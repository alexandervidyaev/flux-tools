package helm

import (
	"encoding/base64"
	"testing"
)

func TestCredentialsFromDockerConfig(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	tests := []struct {
		name     string
		raw      string
		registry string
		wantUser string
		wantPass string
		wantErr  bool
	}{
		{
			name:     "base64 encoded, username/password fields",
			raw:      b64(`{"auths":{"cr.example.com":{"username":"user1","password":"pass1"}}}`),
			registry: "cr.example.com",
			wantUser: "user1",
			wantPass: "pass1",
		},
		{
			name:     "raw JSON (stringData), auth field only",
			raw:      `{"auths":{"cr.example.com":{"auth":"` + b64("user2:pass:with:colons") + `"}}}`,
			registry: "cr.example.com",
			wantUser: "user2",
			wantPass: "pass:with:colons",
		},
		{
			name:     "no entry for registry",
			raw:      b64(`{"auths":{"other.example.com":{"username":"u","password":"p"}}}`),
			registry: "cr.example.com",
			wantErr:  true,
		},
		{
			name:     "entry without usable credentials",
			raw:      b64(`{"auths":{"cr.example.com":{}}}`),
			registry: "cr.example.com",
			wantErr:  true,
		},
		{
			name:     "auth field is not user:password",
			raw:      b64(`{"auths":{"cr.example.com":{"auth":"` + b64("no-colon") + `"}}}`),
			registry: "cr.example.com",
			wantErr:  true,
		},
		{
			name:     "garbage input",
			raw:      "%%% not base64 and not json %%%",
			registry: "cr.example.com",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, pass, err := credentialsFromDockerConfig(tt.raw, tt.registry)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got user=%q pass=%q", user, pass)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if user != tt.wantUser || pass != tt.wantPass {
				t.Errorf("got %q/%q, want %q/%q", user, pass, tt.wantUser, tt.wantPass)
			}
		})
	}
}
