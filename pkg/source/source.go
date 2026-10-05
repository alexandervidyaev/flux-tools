package source

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	fluxexec "github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

func pickVersion(tags []string, want, filter string) (string, error) {
	c, err := semver.NewConstraint(fallback(want, "*"))
	if err != nil {
		return "", err
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return "", err
	}
	var best *semver.Version
	selected := ""
	for _, tag := range tags {
		v, err := semver.NewVersion(tag)
		if err == nil && re.MatchString(tag) && c.Check(v) && (best == nil || v.GreaterThan(best) || (v.Equal(best) && tag > selected)) {
			best = v
			selected = tag
		}
	}
	if best == nil {
		return "", fmt.Errorf("no version matching %q and %q", want, filter)
	}
	return selected, nil
}

func (r *Resolver) resolveOCI(repo string, ref Object) (string, error) {
	k := repo + "|" + stringJSON(ref)
	if v := r.Resolved[k]; v != "" {
		return v, nil
	}
	digest := text(ref, "digest")
	if digest == "" {
		tag := fallback(text(ref, "tag"), "latest")
		if want := text(ref, "semver"); want != "" {
			out, err := r.command(r.Config.Root, nil, "crane", "ls", repo)
			if err != nil {
				return "", err
			}
			tag, err = pickVersion(strings.Fields(string(out)), want, text(ref, "semverFilter"))
			if err != nil {
				return "", err
			}
		}
		out, err := r.command(r.Config.Root, nil, "crane", "digest", repo+":"+tag)
		if err != nil {
			return "", err
		}
		digest = strings.TrimSpace(string(out))
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(digest) {
		return "", fmt.Errorf("invalid OCI digest")
	}
	r.Resolved[k] = repo + "@" + digest
	r.Versions[k] = repo + "@" + digest
	return r.Resolved[k], nil
}
func extract(b io.Reader, dir string) error {
	gz, err := gzip.NewReader(b)
	if err != nil {
		return err
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	for {
		h, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel := filepath.Clean(h.Name)
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe artifact path %q", h.Name)
		}
		dst := filepath.Join(dir, rel)
		switch h.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0700); err != nil {
				return err
			}
		case tar.TypeReg, byte(0):
			if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tarReader)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported artifact entry %q (links are not followed)", h.Name)
		}
	}
}
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks in artifacts are unsupported: %s", rel)
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0600)
	})
}
func (r *Resolver) downloadOCI(repo string, ref Object) (string, error) {
	pinned, err := r.resolveOCI(repo, ref)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(r.Config.Cache, "artifacts", hash(pinned))
	if _, err := os.Stat(filepath.Join(dst, ".complete")); err == nil {
		return dst, nil
	}
	tmp, err := os.MkdirTemp(r.Config.Cache, "artifact-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	manifest, err := r.command(r.Config.Root, nil, "crane", "manifest", pinned)
	if err != nil {
		return "", err
	}
	var m struct {
		Layers []struct{ Digest, MediaType string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return "", err
	}
	if len(m.Layers) == 0 {
		return "", fmt.Errorf("OCI artifact has no layers")
	}
	layer := m.Layers[0].Digest
	for _, l := range m.Layers {
		if strings.Contains(l.MediaType, "flux.content") {
			layer = l.Digest
			break
		}
	}
	out, err := r.command(r.Config.Root, nil, "crane", "blob", repo+"@"+layer)
	if err != nil {
		return "", err
	}
	if err := extract(strings.NewReader(string(out)), tmp); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".complete"), nil, 0600); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}
func (r *Resolver) fetchSource(root string, o Object) (string, error) {
	url := text(o, "spec", "url")
	if operation := text(o, "spec", "layerSelector", "operation"); operation != "" && operation != "extract" {
		return "", fmt.Errorf("unsupported OCI layer operation %s", operation)
	}
	if text(o, "spec", "layerSelector", "mediaType") != "" {
		return "", fmt.Errorf("custom OCI layerSelector requires explicit support")
	}
	localDir := ""
	longest := ""
	for suffix, dir := range r.Config.LocalArtifacts {
		if strings.HasSuffix(url, "/"+suffix) && len(suffix) > len(longest) {
			longest = suffix
			localDir = dir
		}
	}
	var src string
	if localDir != "" {
		src = local(root, localDir)
		r.Versions[url] = "local: " + localDir
	} else {
		var err error
		src, err = r.downloadOCI(strings.TrimPrefix(rewrite(url, r.Config.OCIRewrite), "oci://"), object(o, "spec", "ref"))
		if err != nil {
			return "", err
		}
	}
	dst, err := os.MkdirTemp(r.Config.Work, "source-")
	if err != nil {
		return "", err
	}
	if err := copyTree(src, dst); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(dst, ".complete"))
	rules := []byte(".git/\n.gitignore\n.gitmodules\n.gitattributes\n.sourceignore\n")
	if b, err := os.ReadFile(filepath.Join(dst, ".sourceignore")); err == nil {
		rules = append(rules, b...)
		rules = append(rules, '\n')
	}
	rules = append(rules, []byte(text(o, "spec", "ignore"))...)
	rulePath := filepath.Join(r.Config.Work, "ignore-"+hash(dst))
	if err := os.WriteFile(rulePath, rules, 0600); err != nil {
		return "", err
	}
	if _, err := r.command(dst, nil, "git", "init", "-q"); err != nil {
		return "", err
	}
	list, err := r.command(dst, nil, "git", "-c", "safe.directory=*", "ls-files", "-o", "-i", "--exclude-from="+rulePath, "-z")
	if err != nil {
		return "", err
	}
	for _, p := range strings.Split(string(list), "\x00") {
		if p != "" {
			if err := os.Remove(filepath.Join(dst, p)); err != nil {
				return "", err
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(dst, ".git"))
	return dst, nil
}
func mergeFiles(a, b []byte) ([]byte, error) {
	left, err := documents(a)
	if err != nil {
		return nil, err
	}
	right, err := documents(b)
	if err != nil {
		return nil, err
	}
	result := Object{}
	for _, o := range append(left, right...) {
		result = merge(result, o)
	}
	return yaml.Marshal(result)
}
func (r *Resolver) external(root string, set []Item, n, ns string) (string, error) {
	var generator, artifact Object
	for _, it := range set {
		if text(it.Object, "kind") != "ArtifactGenerator" || namespace(it.Object) != ns {
			continue
		}
		for _, v := range array(it.Object, "spec", "artifacts") {
			a, ok := v.(map[string]any)
			if ok && text(a, "name") == n {
				generator = it.Object
				artifact = a
				break
			}
		}
	}
	if generator == nil {
		return "", fmt.Errorf("ArtifactGenerator for %s/%s not found", ns, n)
	}
	dirs := map[string]string{}
	for _, v := range array(generator, "spec", "sources") {
		s, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("ArtifactGenerator source must be an object")
		}
		if text(s, "kind") != "OCIRepository" {
			return "", fmt.Errorf("unsupported ArtifactGenerator source %s", text(s, "kind"))
		}
		o := find(set, "OCIRepository", fallback(text(s, "namespace"), ns), text(s, "name"))
		if o == nil {
			return "", fmt.Errorf("ArtifactGenerator source %s not found", text(s, "name"))
		}
		d, err := r.fetchSource(root, o)
		if err != nil {
			return "", err
		}
		dirs[text(s, "alias")] = d
	}
	out, err := os.MkdirTemp(r.Config.Work, "external-")
	if err != nil {
		return "", err
	}
	for _, v := range array(artifact, "copy") {
		op, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("ArtifactGenerator copy must be an object")
		}
		from := strings.TrimPrefix(text(op, "from"), "@")
		alias, pattern, _ := strings.Cut(from, "/")
		pattern = fallback(pattern, "**")
		src := dirs[alias]
		if src == "" {
			return "", fmt.Errorf("source alias %s not found", alias)
		}
		strategy := fallback(text(op, "strategy"), "Overwrite")
		if strategy != "Overwrite" && strategy != "Merge" {
			return "", fmt.Errorf("unsupported copy strategy %s", strategy)
		}
		paths, err := doublestar.Glob(os.DirFS(src), pattern)
		if err != nil {
			return "", err
		}
		sort.Strings(paths)
		prefix := pattern
		if i := strings.IndexAny(prefix, "*?["); i >= 0 {
			prefix = prefix[:i]
		}
		if i := strings.LastIndex(prefix, "/"); i >= 0 {
			prefix = prefix[:i+1]
		} else {
			prefix = ""
		}
		destination := text(op, "to")
		if destination != "@artifact" && !strings.HasPrefix(destination, "@artifact/") {
			return "", fmt.Errorf("invalid artifact destination %q", destination)
		}
		to := strings.TrimPrefix(strings.TrimPrefix(destination, "@artifact"), "/")
		for _, p := range paths {
			info, err := os.Stat(filepath.Join(src, p))
			if err != nil {
				return "", err
			}
			if info.IsDir() {
				continue
			}
			excluded := false
			for _, x := range array(op, "exclude") {
				if match, err := doublestar.Match(fmt.Sprint(x), p); err != nil {
					return "", err
				} else if match {
					excluded = true
				}
			}
			if excluded {
				continue
			}
			target := to
			if to == "" || strings.HasSuffix(to, "/") || pattern != p {
				target = filepath.Join(to, strings.TrimPrefix(p, prefix))
			}
			if filepath.IsAbs(target) || target == ".." || strings.HasPrefix(filepath.Clean(target), "../") {
				return "", fmt.Errorf("unsafe copy target")
			}
			dest := filepath.Join(out, target)
			b, err := os.ReadFile(filepath.Join(src, p))
			if err != nil {
				return "", err
			}
			if strategy == "Merge" {
				if a, err := os.ReadFile(dest); err == nil {
					b, err = mergeFiles(a, b)
					if err != nil {
						return "", err
					}
				} else if !os.IsNotExist(err) {
					return "", err
				}
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return "", err
			}
			if err := os.WriteFile(dest, b, 0600); err != nil {
				return "", err
			}
		}
	}
	return out, nil
}

type Config struct {
	Root, Cache, Work          string
	LocalArtifacts, OCIRewrite map[string]string
}
type Resolver struct {
	Config             Config
	Resolved, Versions map[string]string
	objects            []Item
	ctx                context.Context
}

func New(ctx context.Context, root, cache string, localArtifacts, rewrites map[string]string) (*Resolver, error) {
	if err := os.MkdirAll(cache, 0700); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(cache, "sources-")
	if err != nil {
		return nil, err
	}
	return &Resolver{Config: Config{Root: root, Cache: work, Work: work, LocalArtifacts: localArtifacts, OCIRewrite: rewrites}, Resolved: map[string]string{}, Versions: map[string]string{}, ctx: ctx}, nil
}
func (r *Resolver) Close() error { return os.RemoveAll(r.Config.Work) }
func (r *Resolver) Register(o Object) {
	for i, it := range r.objects {
		if text(it.Object, "kind") == text(o, "kind") && namespace(it.Object) == namespace(o) && text(it.Object, "metadata", "name") == text(o, "metadata", "name") {
			r.objects[i].Object = o
			return
		}
	}
	r.objects = append(r.objects, Item{Object: o})
}
func (r *Resolver) Resolve(kind, ns, name string) (string, error) {
	if kind == "ExternalArtifact" {
		return r.external(r.Config.Root, r.objects, name, ns)
	}
	if kind != "OCIRepository" {
		return "", fmt.Errorf("unsupported source kind %s", kind)
	}
	o := find(r.objects, kind, ns, name)
	if o == nil {
		return "", fmt.Errorf("source %s %s/%s not found", kind, ns, name)
	}
	return r.fetchSource(r.Config.Root, o)
}
func (r *Resolver) command(dir string, env []string, name string, args ...string) ([]byte, error) {
	return (&fluxexec.RealRunner{Dir: dir, Env: env}).Run(r.ctx, name, args...)
}
func hash(s string) string       { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func stringJSON(o Object) string { b, _ := json.Marshal(o); return string(b) }
func local(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}
func rewrite(url string, rules map[string]string) string {
	longest := ""
	replacement := ""
	for from, to := range rules {
		if strings.HasPrefix(url, from) && len(from) > len(longest) {
			longest = from
			replacement = to
		}
	}
	if longest != "" {
		return replacement + strings.TrimPrefix(url, longest)
	}
	return url
}
