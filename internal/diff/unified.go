package diff

// unified.go generates unified diff hunks in pure Go, replacing the
// per-file-pair `git diff --no-index` subprocess in compareFiles.
//
// It is a faithful port of git's xdiff backend, following git v2.50.1
// xdiff/{xprepare,xdiffi,xemit,xutils}.c (LibXDiff by Davide Libenzi,
// LGPL-2.1): the Myers algorithm with the libxdiff cost/heuristic cuts,
// frequent-record cleanup, change compaction with the indent heuristic, and
// hunk emission. The goal is byte-for-byte parity with
// `git diff --no-index -U<n> --no-color` on the diff content lines
// (context/-/+ lines, hunk grouping, and the "\ No newline at end of file"
// marker). File headers (diff --git/index/---/+++) and the @@ hunk header
// markup are free-form: CleanDiffLines strips them before any report,
// comment or artifact is produced. Parity is locked by the golden tests in
// unified_test.go, which compare against a real git binary.

import (
	"bytes"
	"math"
	"strings"
)

// Constants from xdiffi.c / xprepare.c, same names and values as in git.
const (
	xdlMaxCostMin    = 256
	xdlHeurMinCost   = 256
	xdlSnakeCnt      = 20
	xdlKHeur         = 4
	xdlKpdisRun      = 4
	xdlMaxEqLimit    = 1024
	xdlSimScanWindow = 100
)

// Indent-heuristic constants from xdiffi.c (diff-slider-tools corpus).
const (
	maxIndent = 200
	maxBlanks = 20

	startOfFilePenalty              = 1
	endOfFilePenalty                = 21
	totalBlankWeight                = -30
	postBlankWeight                 = 6
	relativeIndentPenalty           = -4
	relativeIndentWithBlankPenalty  = 10
	relativeOutdentPenalty          = 24
	relativeOutdentWithBlankPenalty = 17
	relativeDedentPenalty           = 23
	relativeDedentWithBlankPenalty  = 17
	indentWeight                    = 60
	indentHeuristicMaxSliding       = 100
)

// xdfile mirrors git's xdfile_t for one side of the diff.
type xdfile struct {
	lines []string // raw line content, including the trailing "\n" when present
	cls   []int    // class id per line: equal ids <=> byte-identical lines
	chg   []bool   // change flags, index shifted by 1 (sentinels at -1 and nrec)

	// Effective (non-discarded) records inside [dstart, dend], filled by
	// cleanupRecords. rindex maps effective index -> line index, eha holds
	// the class ids the Myers core compares.
	rindex       []int
	eha          []int
	nreff        int
	dstart, dend int
}

func (f *xdfile) nrec() int                { return len(f.lines) }
func (f *xdfile) changed(i int) bool       { return f.chg[i+1] }
func (f *xdfile) setChanged(i int, v bool) { f.chg[i+1] = v }

// unifiedDiff returns the unified diff hunks between two file contents with
// the given number of context lines. The result contains hunks only, no file
// headers; it is empty when the inputs are line-identical.
func unifiedDiff(current, incoming []byte, contextLines int) string {
	x1, x2 := prepareEnv(current, incoming)
	doDiff(x1, x2)
	changeCompact(x1, x2)
	changeCompact(x2, x1)
	script := buildScript(x1, x2)
	if len(script) == 0 {
		return ""
	}
	var out strings.Builder
	emitDiff(x1, x2, script, contextLines, &out)
	return out.String()
}

// splitLines splits data into lines, keeping the trailing "\n" of each line
// (the last line may lack it), matching xdiff record boundaries.
func splitLines(data []byte) []string {
	var lines []string
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			lines = append(lines, string(data))
			break
		}
		lines = append(lines, string(data[:i+1]))
		data = data[i+1:]
	}
	return lines
}

// prepareEnv is the xdl_prepare_env port: it classifies lines (equal class
// id <=> identical content, newline included), trims common ends and runs
// the record cleanup, exactly like xdl_optimize_ctxs.
func prepareEnv(current, incoming []byte) (*xdfile, *xdfile) {
	lines1 := splitLines(current)
	lines2 := splitLines(incoming)

	classIDs := make(map[string]int, len(lines1)+len(lines2))
	var cnt1, cnt2 []int // occurrences of each class in file1 / file2
	classify := func(lines []string, cnt *[]int) []int {
		cls := make([]int, len(lines))
		for i, l := range lines {
			id, ok := classIDs[l]
			if !ok {
				id = len(cnt1)
				classIDs[l] = id
				cnt1 = append(cnt1, 0)
				cnt2 = append(cnt2, 0)
			}
			cls[i] = id
			(*cnt)[id]++
		}
		return cls
	}
	cls1 := classify(lines1, &cnt1)
	cls2 := classify(lines2, &cnt2)

	x1 := &xdfile{lines: lines1, cls: cls1, chg: make([]bool, len(lines1)+2)}
	x2 := &xdfile{lines: lines2, cls: cls2, chg: make([]bool, len(lines2)+2)}

	trimEnds(x1, x2)
	cleanupRecords(x1, x2, cnt1, cnt2)
	return x1, x2
}

// trimEnds is the xdl_trim_ends port: early trim of matching prefix/suffix.
func trimEnds(x1, x2 *xdfile) {
	n1, n2 := x1.nrec(), x2.nrec()
	lim := n1
	if n2 < lim {
		lim = n2
	}
	i := 0
	for ; i < lim; i++ {
		if x1.cls[i] != x2.cls[i] {
			break
		}
	}
	x1.dstart, x2.dstart = i, i

	lim -= i
	j := 0
	for ; j < lim; j++ {
		if x1.cls[n1-1-j] != x2.cls[n2-1-j] {
			break
		}
	}
	x1.dend = n1 - j - 1
	x2.dend = n2 - j - 1
}

// bogosqrt is xdl_bogosqrt: classical integer square root approximation.
func bogosqrt(n int) int {
	i := 1
	for ; n > 0; n >>= 2 {
		i <<= 1
	}
	return i
}

// cleanupRecords is the xdl_cleanup_records port: records with no match on
// the other side are pre-marked as changed, records that occur too often
// (>= bogosqrt(nrec)) inside runs of unmatched lines are discarded from the
// Myers core to keep it fast; the rest become the effective sequences.
func cleanupRecords(x1, x2 *xdfile, cnt1, cnt2 []int) {
	dis1 := make([]int8, x1.nrec()+1)
	dis2 := make([]int8, x2.nrec()+1)

	fill := func(x *xdfile, other []int, dis []int8) {
		mlim := bogosqrt(x.nrec())
		if mlim > xdlMaxEqLimit {
			mlim = xdlMaxEqLimit
		}
		for i := x.dstart; i <= x.dend; i++ {
			nm := other[x.cls[i]]
			switch {
			case nm == 0:
				dis[i] = 0
			case nm >= mlim:
				dis[i] = 2
			default:
				dis[i] = 1
			}
		}
	}
	fill(x1, cnt2, dis1)
	fill(x2, cnt1, dis2)

	reduce := func(x *xdfile, dis []int8) {
		for i := x.dstart; i <= x.dend; i++ {
			if dis[i] == 1 || (dis[i] == 2 && !cleanMmatch(dis, i, x.dstart, x.dend)) {
				x.rindex = append(x.rindex, i)
				x.eha = append(x.eha, x.cls[i])
			} else {
				x.setChanged(i, true)
			}
		}
		x.nreff = len(x.rindex)
	}
	reduce(x1, dis1)
	reduce(x2, dis2)
}

// cleanMmatch is the xdl_clean_mmatch port: decides whether a multimatch
// line in the middle of runs of unmatched lines should be discarded.
func cleanMmatch(dis []int8, i, s, e int) bool {
	if i-s > xdlSimScanWindow {
		s = i - xdlSimScanWindow
	}
	if e-i > xdlSimScanWindow {
		e = i + xdlSimScanWindow
	}

	rdis0, rpdis0 := 0, 1
	for r := 1; i-r >= s; r++ {
		if dis[i-r] == 0 {
			rdis0++
		} else if dis[i-r] == 2 {
			rpdis0++
		} else {
			break
		}
	}
	if rdis0 == 0 {
		return false
	}
	rdis1, rpdis1 := 0, 1
	for r := 1; i+r <= e; r++ {
		if dis[i+r] == 0 {
			rdis1++
		} else if dis[i+r] == 2 {
			rpdis1++
		} else {
			break
		}
	}
	if rdis1 == 0 {
		return false
	}
	rdis1 += rdis0
	rpdis1 += rpdis0

	return rpdis1*xdlKpdisRun < rpdis1+rdis1
}

// algoEnv mirrors xdalgoenv_t.
type algoEnv struct {
	mxcost   int
	snakeCnt int
	heurMin  int
}

// splitResult mirrors xdpsplit_t.
type splitResult struct {
	i1, i2       int
	minLo, minHi bool
}

// doDiff is the xdl_do_diff port for the default (Myers) algorithm.
func doDiff(x1, x2 *xdfile) {
	ndiags := x1.nreff + x2.nreff + 3
	kvdf := make([]int, ndiags)
	kvdb := make([]int, ndiags)
	koff := x2.nreff + 1

	env := algoEnv{
		mxcost:   bogosqrt(ndiags),
		snakeCnt: xdlSnakeCnt,
		heurMin:  xdlHeurMinCost,
	}
	if env.mxcost < xdlMaxCostMin {
		env.mxcost = xdlMaxCostMin
	}

	recsCmp(x1, 0, x1.nreff, x2, 0, x2.nreff, kvdf, kvdb, koff, false, &env)
}

// recsCmp is the xdl_recs_cmp port: divide and conquer over the effective
// sequences, marking changed lines through rindex.
func recsCmp(x1 *xdfile, off1, lim1 int, x2 *xdfile, off2, lim2 int,
	kvdf, kvdb []int, koff int, needMin bool, env *algoEnv) {
	ha1, ha2 := x1.eha, x2.eha

	for off1 < lim1 && off2 < lim2 && ha1[off1] == ha2[off2] {
		off1++
		off2++
	}
	for off1 < lim1 && off2 < lim2 && ha1[lim1-1] == ha2[lim2-1] {
		lim1--
		lim2--
	}

	switch {
	case off1 == lim1:
		for ; off2 < lim2; off2++ {
			x2.setChanged(x2.rindex[off2], true)
		}
	case off2 == lim2:
		for ; off1 < lim1; off1++ {
			x1.setChanged(x1.rindex[off1], true)
		}
	default:
		spl := xdlSplit(ha1, off1, lim1, ha2, off2, lim2, kvdf, kvdb, koff, needMin, env)
		recsCmp(x1, off1, spl.i1, x2, off2, spl.i2, kvdf, kvdb, koff, spl.minLo, env)
		recsCmp(x1, spl.i1, lim1, x2, spl.i2, lim2, kvdf, kvdb, koff, spl.minHi, env)
	}
}

// xdlSplit is the xdl_split port: bidirectional Myers with the libxdiff
// "good snake" and max-cost cuts that git applies by default (no
// XDF_NEED_MINIMAL). kvdf/kvdb are indexed by diagonal+koff.
func xdlSplit(ha1 []int, off1, lim1 int, ha2 []int, off2, lim2 int,
	kvdf, kvdb []int, koff int, needMin bool, env *algoEnv) splitResult {
	var spl splitResult
	dmin, dmax := off1-lim2, lim1-off2
	fmid, bmid := off1-off2, lim1-lim2
	odd := (fmid-bmid)&1 != 0
	fmin, fmax := fmid, fmid
	bmin, bmax := bmid, bmid

	kvdf[fmid+koff] = off1
	kvdb[bmid+koff] = lim1

	for ec := 1; ; ec++ {
		gotSnake := false

		if fmin > dmin {
			fmin--
			kvdf[fmin-1+koff] = -1
		} else {
			fmin++
		}
		if fmax < dmax {
			fmax++
			kvdf[fmax+1+koff] = -1
		} else {
			fmax--
		}

		for d := fmax; d >= fmin; d -= 2 {
			var i1 int
			if kvdf[d-1+koff] >= kvdf[d+1+koff] {
				i1 = kvdf[d-1+koff] + 1
			} else {
				i1 = kvdf[d+1+koff]
			}
			prev1 := i1
			i2 := i1 - d
			for i1 < lim1 && i2 < lim2 && ha1[i1] == ha2[i2] {
				i1++
				i2++
			}
			if i1-prev1 > env.snakeCnt {
				gotSnake = true
			}
			kvdf[d+koff] = i1
			if odd && bmin <= d && d <= bmax && kvdb[d+koff] <= i1 {
				spl.i1, spl.i2 = i1, i2
				spl.minLo, spl.minHi = true, true
				return spl
			}
		}

		if bmin > dmin {
			bmin--
			kvdb[bmin-1+koff] = math.MaxInt
		} else {
			bmin++
		}
		if bmax < dmax {
			bmax++
			kvdb[bmax+1+koff] = math.MaxInt
		} else {
			bmax--
		}

		for d := bmax; d >= bmin; d -= 2 {
			var i1 int
			if kvdb[d-1+koff] < kvdb[d+1+koff] {
				i1 = kvdb[d-1+koff]
			} else {
				i1 = kvdb[d+1+koff] - 1
			}
			prev1 := i1
			i2 := i1 - d
			for i1 > off1 && i2 > off2 && ha1[i1-1] == ha2[i2-1] {
				i1--
				i2--
			}
			if prev1-i1 > env.snakeCnt {
				gotSnake = true
			}
			kvdb[d+koff] = i1
			if !odd && fmin <= d && d <= fmax && i1 <= kvdf[d+koff] {
				spl.i1, spl.i2 = i1, i2
				spl.minLo, spl.minHi = true, true
				return spl
			}
		}

		if needMin {
			continue
		}

		// Heuristic: with a good snake and a high enough cost, cut at an
		// "interesting" far-reaching diagonal (see xdl_split comments).
		if gotSnake && ec > env.heurMin {
			best := 0
			for d := fmax; d >= fmin; d -= 2 {
				dd := d - fmid
				if dd < 0 {
					dd = -dd
				}
				i1 := kvdf[d+koff]
				i2 := i1 - d
				v := (i1 - off1) + (i2 - off2) - dd

				if v > xdlKHeur*ec && v > best &&
					off1+env.snakeCnt <= i1 && i1 < lim1 &&
					off2+env.snakeCnt <= i2 && i2 < lim2 {
					for k := 1; ha1[i1-k] == ha2[i2-k]; k++ {
						if k == env.snakeCnt {
							best = v
							spl.i1, spl.i2 = i1, i2
							break
						}
					}
				}
			}
			if best > 0 {
				spl.minLo, spl.minHi = true, false
				return spl
			}

			best = 0
			for d := bmax; d >= bmin; d -= 2 {
				dd := d - bmid
				if dd < 0 {
					dd = -dd
				}
				i1 := kvdb[d+koff]
				i2 := i1 - d
				v := (lim1 - i1) + (lim2 - i2) - dd

				if v > xdlKHeur*ec && v > best &&
					off1 < i1 && i1 <= lim1-env.snakeCnt &&
					off2 < i2 && i2 <= lim2-env.snakeCnt {
					for k := 0; ha1[i1+k] == ha2[i2+k]; k++ {
						if k == env.snakeCnt-1 {
							best = v
							spl.i1, spl.i2 = i1, i2
							break
						}
					}
				}
			}
			if best > 0 {
				spl.minLo, spl.minHi = false, true
				return spl
			}
		}

		// Cost cap reached: pick the furthest reaching path.
		if ec >= env.mxcost {
			fbest, fbest1 := -1, -1
			for d := fmax; d >= fmin; d -= 2 {
				i1 := kvdf[d+koff]
				if lim1 < i1 {
					i1 = lim1
				}
				i2 := i1 - d
				if lim2 < i2 {
					i1 = lim2 + d
					i2 = lim2
				}
				if fbest < i1+i2 {
					fbest = i1 + i2
					fbest1 = i1
				}
			}

			bbest, bbest1 := math.MaxInt, math.MaxInt
			for d := bmax; d >= bmin; d -= 2 {
				i1 := kvdb[d+koff]
				if i1 < off1 {
					i1 = off1
				}
				i2 := i1 - d
				if i2 < off2 {
					i1 = off2 + d
					i2 = off2
				}
				if i1+i2 < bbest {
					bbest = i1 + i2
					bbest1 = i1
				}
			}

			if (lim1+lim2)-bbest < fbest-(off1+off2) {
				spl.i1 = fbest1
				spl.i2 = fbest - fbest1
				spl.minLo, spl.minHi = true, false
			} else {
				spl.i1 = bbest1
				spl.i2 = bbest - bbest1
				spl.minLo, spl.minHi = false, true
			}
			return spl
		}
	}
}

// xdlgroup mirrors struct xdlgroup: a contiguous group of changed lines.
type xdlgroup struct {
	start, end int
}

func groupInit(f *xdfile, g *xdlgroup) {
	g.start, g.end = 0, 0
	for f.changed(g.end) {
		g.end++
	}
}

// groupNext reports whether g was moved to the next (possibly empty) group.
func groupNext(f *xdfile, g *xdlgroup) bool {
	if g.end == f.nrec() {
		return false
	}
	g.start = g.end + 1
	for g.end = g.start; f.changed(g.end); g.end++ {
	}
	return true
}

// groupPrevious reports whether g was moved to the previous group.
func groupPrevious(f *xdfile, g *xdlgroup) bool {
	if g.start == 0 {
		return false
	}
	g.end = g.start - 1
	for g.start = g.end; f.changed(g.start - 1); g.start-- {
	}
	return true
}

// groupSlideDown slides g toward the end of the file, merging with a
// following group it bumps into. Reports whether the slide happened.
func groupSlideDown(f *xdfile, g *xdlgroup) bool {
	if g.end < f.nrec() && f.cls[g.start] == f.cls[g.end] {
		f.setChanged(g.start, false)
		g.start++
		f.setChanged(g.end, true)
		g.end++
		for f.changed(g.end) {
			g.end++
		}
		return true
	}
	return false
}

// groupSlideUp slides g toward the beginning of the file, merging with a
// previous group it bumps into. Reports whether the slide happened.
func groupSlideUp(f *xdfile, g *xdlgroup) bool {
	if g.start > 0 && f.cls[g.start-1] == f.cls[g.end-1] {
		g.start--
		f.setChanged(g.start, true)
		g.end--
		f.setChanged(g.end, false)
		for f.changed(g.start - 1) {
			g.start--
		}
		return true
	}
	return false
}

// changeCompact is the xdl_change_compact port with XDF_INDENT_HEURISTIC
// enabled, matching git's default since 2.14: slide change groups for a
// consistent, pretty diff, preferring positions that align with changes in
// the other file or score best under the indent heuristic.
func changeCompact(f, fo *xdfile) {
	var g, og xdlgroup
	groupInit(f, &g)
	groupInit(fo, &og)

	for {
		if g.end > g.start {
			var earliestEnd int
			endMatchingOther := -1
			groupsize := 0

			for {
				groupsize = g.end - g.start
				endMatchingOther = -1

				// Shift the group backward as much as possible.
				for groupSlideUp(f, &g) {
					if !groupPrevious(fo, &og) {
						panic("xdiff: group sync broken sliding up")
					}
				}
				earliestEnd = g.end
				if og.end > og.start {
					endMatchingOther = g.end
				}

				// Now shift the group forward as far as possible.
				for {
					if !groupSlideDown(f, &g) {
						break
					}
					if !groupNext(fo, &og) {
						panic("xdiff: group sync broken sliding down")
					}
					if og.end > og.start {
						endMatchingOther = g.end
					}
				}

				if groupsize == g.end-g.start {
					break
				}
			}

			if g.end == earliestEnd {
				// No shifting was possible.
			} else if endMatchingOther != -1 {
				// Move the group back to line up with the last group of
				// changes from the other file that it can align with.
				for og.end == og.start {
					if !groupSlideUp(f, &g) {
						panic("xdiff: match disappeared")
					}
					if !groupPrevious(fo, &og) {
						panic("xdiff: group sync broken sliding to match")
					}
				}
			} else {
				// Indent heuristic: pick the shift whose two split
				// positions score best.
				shift := earliestEnd
				if g.end-groupsize-1 > shift {
					shift = g.end - groupsize - 1
				}
				if g.end-indentHeuristicMaxSliding > shift {
					shift = g.end - indentHeuristicMaxSliding
				}
				bestShift := -1
				var bestScore splitScore
				for ; shift <= g.end; shift++ {
					var m splitMeasurement
					var score splitScore
					measureSplit(f, shift, &m)
					scoreAddSplit(&m, &score)
					measureSplit(f, shift-groupsize, &m)
					scoreAddSplit(&m, &score)
					if bestShift == -1 || scoreCmp(&score, &bestScore) <= 0 {
						bestScore = score
						bestShift = shift
					}
				}

				for g.end > bestShift {
					if !groupSlideUp(f, &g) {
						panic("xdiff: best shift unreached")
					}
					if !groupPrevious(fo, &og) {
						panic("xdiff: group sync broken sliding to blank line")
					}
				}
			}
		}

		// Move past the just-processed group.
		if !groupNext(f, &g) {
			break
		}
		if !groupNext(fo, &og) {
			panic("xdiff: group sync broken moving to next group")
		}
	}

	if groupNext(fo, &og) {
		panic("xdiff: group sync broken at end of file")
	}
}

// splitMeasurement mirrors struct split_measurement.
type splitMeasurement struct {
	endOfFile  bool
	indent     int
	preBlank   int
	preIndent  int
	postBlank  int
	postIndent int
}

// splitScore mirrors struct split_score.
type splitScore struct {
	effectiveIndent int
	penalty         int
}

// isXdlSpace matches XDL_ISSPACE (C isspace in the default locale).
func isXdlSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// getIndent is the get_indent port: indentation width with TAB as 8 columns,
// -1 for blank lines, clamped at maxIndent.
func getIndent(line string) int {
	ret := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if !isXdlSpace(c) {
			return ret
		} else if c == ' ' {
			ret++
		} else if c == '\t' {
			ret += 8 - ret%8
		}
		// Other whitespace characters are ignored.
		if ret >= maxIndent {
			return maxIndent
		}
	}
	return -1
}

// measureSplit is the measure_split port.
func measureSplit(f *xdfile, split int, m *splitMeasurement) {
	if split >= f.nrec() {
		m.endOfFile = true
		m.indent = -1
	} else {
		m.endOfFile = false
		m.indent = getIndent(f.lines[split])
	}

	m.preBlank = 0
	m.preIndent = -1
	for i := split - 1; i >= 0; i-- {
		m.preIndent = getIndent(f.lines[i])
		if m.preIndent != -1 {
			break
		}
		m.preBlank++
		if m.preBlank == maxBlanks {
			m.preIndent = 0
			break
		}
	}

	m.postBlank = 0
	m.postIndent = -1
	for i := split + 1; i < f.nrec(); i++ {
		m.postIndent = getIndent(f.lines[i])
		if m.postIndent != -1 {
			break
		}
		m.postBlank++
		if m.postBlank == maxBlanks {
			m.postIndent = 0
			break
		}
	}
}

// scoreAddSplit is the score_add_split port.
func scoreAddSplit(m *splitMeasurement, s *splitScore) {
	if m.preIndent == -1 && m.preBlank == 0 {
		s.penalty += startOfFilePenalty
	}
	if m.endOfFile {
		s.penalty += endOfFilePenalty
	}

	postBlank := 0
	if m.indent == -1 {
		postBlank = 1 + m.postBlank
	}
	totalBlank := m.preBlank + postBlank

	s.penalty += totalBlankWeight * totalBlank
	s.penalty += postBlankWeight * postBlank

	indent := m.indent
	if indent == -1 {
		indent = m.postIndent
	}

	anyBlanks := totalBlank != 0

	// The effective indent is -1 at the end of the file.
	s.effectiveIndent += indent

	switch {
	case indent == -1:
		// No additional adjustments needed.
	case m.preIndent == -1:
		// No additional adjustments needed.
	case indent > m.preIndent:
		if anyBlanks {
			s.penalty += relativeIndentWithBlankPenalty
		} else {
			s.penalty += relativeIndentPenalty
		}
	case indent == m.preIndent:
		// Same indentation as the predecessor, no adjustments.
	default:
		// Indented less than the predecessor: block start vs block end.
		if m.postIndent != -1 && m.postIndent > indent {
			if anyBlanks {
				s.penalty += relativeOutdentWithBlankPenalty
			} else {
				s.penalty += relativeOutdentPenalty
			}
		} else {
			if anyBlanks {
				s.penalty += relativeDedentWithBlankPenalty
			} else {
				s.penalty += relativeDedentPenalty
			}
		}
	}
}

// scoreCmp is the score_cmp port.
func scoreCmp(s1, s2 *splitScore) int {
	cmpIndents := 0
	if s1.effectiveIndent > s2.effectiveIndent {
		cmpIndents = 1
	} else if s1.effectiveIndent < s2.effectiveIndent {
		cmpIndents = -1
	}
	return indentWeight*cmpIndents + (s1.penalty - s2.penalty)
}

// xdchange mirrors xdchange_t (the ignore flag is always false here: no
// --ignore-blank-lines / -I regexes in this pipeline).
type xdchange struct {
	i1, i2     int
	chg1, chg2 int
}

// buildScript is the xdl_build_script port: collect groups of changed lines
// into an edit script, ordered by position.
func buildScript(x1, x2 *xdfile) []xdchange {
	var script []xdchange
	i1, i2 := x1.nrec(), x2.nrec()
	for i1 >= 0 || i2 >= 0 {
		if x1.changed(i1-1) || x2.changed(i2-1) {
			l1, l2 := i1, i2
			for x1.changed(i1 - 1) {
				i1--
			}
			for x2.changed(i2 - 1) {
				i2--
			}
			script = append(script, xdchange{i1, i2, l1 - i1, l2 - i2})
		}
		i1--
		i2--
	}
	// The scan runs backward; restore positional order.
	for i, j := 0, len(script)-1; i < j; i, j = i+1, j-1 {
		script[i], script[j] = script[j], script[i]
	}
	return script
}

// getHunk is the xdl_get_hunk port for the no-ignored-changes case: starting
// at script[i], return the index of the last change to include in the hunk.
// Git merges adjacent changes while the gap of unchanged lines between them
// is at most 2*ctxlen (interhunkctxlen is 0 for git diff).
func getHunk(script []xdchange, i, ctxlen int) int {
	maxCommon := 2 * ctxlen
	last := i
	for j := i + 1; j < len(script); j++ {
		distance := script[j].i1 - (script[j-1].i1 + script[j-1].chg1)
		if distance > maxCommon {
			break
		}
		last = j
	}
	return last
}

// emitDiff is the xdl_emit_diff port (without func-context/func-names, which
// only decorate the @@ header that CleanDiffLines strips).
func emitDiff(x1, x2 *xdfile, script []xdchange, ctxlen int, out *strings.Builder) {
	for i := 0; i < len(script); {
		end := getHunk(script, i, ctxlen)
		xch, xche := script[i], script[end]

		s1 := xch.i1 - ctxlen
		if s1 < 0 {
			s1 = 0
		}
		s2 := xch.i2 - ctxlen
		if s2 < 0 {
			s2 = 0
		}

		lctx := ctxlen
		if n := x1.nrec() - (xche.i1 + xche.chg1); n < lctx {
			lctx = n
		}
		if n := x2.nrec() - (xche.i2 + xche.chg2); n < lctx {
			lctx = n
		}
		e1 := xche.i1 + xche.chg1 + lctx
		e2 := xche.i2 + xche.chg2 + lctx

		writeHunkHeader(out, s1+1, e1-s1, s2+1, e2-s2)

		// Pre-context.
		for ; s2 < xch.i2; s2++ {
			emitRecord(out, " ", x2.lines[s2])
		}

		c1, c2 := xch.i1, xch.i2
		for j := i; ; j++ {
			xc := script[j]
			// Context between merged change atoms.
			for ; c1 < xc.i1 && c2 < xc.i2; c1, c2 = c1+1, c2+1 {
				emitRecord(out, " ", x2.lines[c2])
			}
			// Removed lines from the first file.
			for c1 = xc.i1; c1 < xc.i1+xc.chg1; c1++ {
				emitRecord(out, "-", x1.lines[c1])
			}
			// Added lines from the second file.
			for c2 = xc.i2; c2 < xc.i2+xc.chg2; c2++ {
				emitRecord(out, "+", x2.lines[c2])
			}
			if j == end {
				break
			}
			c1 = xc.i1 + xc.chg1
			c2 = xc.i2 + xc.chg2
		}

		// Post-context.
		for c2 = xche.i2 + xche.chg2; c2 < e2; c2++ {
			emitRecord(out, " ", x2.lines[c2])
		}

		i = end + 1
	}
}

// writeHunkHeader formats "@@ -s1,c1 +s2,c2 @@" with git's rules: a count of
// one is omitted, a zero count shifts the start back by one.
func writeHunkHeader(out *strings.Builder, s1, c1, s2, c2 int) {
	out.WriteString("@@ -")
	writeHunkRange(out, s1, c1)
	out.WriteString(" +")
	writeHunkRange(out, s2, c2)
	out.WriteString(" @@\n")
}

func writeHunkRange(out *strings.Builder, s, c int) {
	start := s
	if c == 0 {
		start = s - 1
	}
	writeInt(out, start)
	if c != 1 {
		out.WriteByte(',')
		writeInt(out, c)
	}
}

func writeInt(out *strings.Builder, v int) {
	var buf [20]byte
	if v < 0 {
		out.WriteByte('-')
		v = -v
	}
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	out.Write(buf[i:])
}

// emitRecord is the xdl_emit_diffrec port: prefix plus line, with the
// "\ No newline at end of file" marker when the line lacks a newline.
func emitRecord(out *strings.Builder, prefix, line string) {
	out.WriteString(prefix)
	out.WriteString(line)
	if len(line) > 0 && line[len(line)-1] != '\n' {
		out.WriteString("\n\\ No newline at end of file\n")
	}
}
