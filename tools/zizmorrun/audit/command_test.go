package audit

import (
	"slices"
	"testing"
)

func TestBuildArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		want  []string
	}{
		{name: "without a token: offline", token: "", want: []string{
			"--offline", "--no-config", "--collect=all", "--strict-collection", "--no-progress",
			"--no-ignores", "--no-exit-codes", "--format", "json-v1", ".",
		}},
		{name: "with a token: online", token: "secret", want: []string{
			"--no-config", "--collect=all", "--strict-collection", "--no-progress",
			"--no-ignores", "--no-exit-codes", "--format", "json-v1", ".",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(tc.token, nil)
			if got.Name != "zizmor" || !slices.Equal(got.Args, tc.want) {
				t.Errorf("Build = %q %q, want zizmor %q", got.Name, got.Args, tc.want)
			}
		})
	}
}

// Each variable that changes what zizmor audits, or where it sends the token, is removed, whatever its value,
// every time it is set, and when it is set only by name. The list is written out here and not read from the
// code under test.
func TestBuildClearsEachVariable(t *testing.T) {
	for _, name := range []string{
		"ZIZMOR_OFFLINE", "ZIZMOR_NO_ONLINE_AUDITS", "ZIZMOR_CONFIG", "GH_HOST", "ZIZMOR_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN",
	} {
		t.Run(name, func(t *testing.T) {
			environ := []string{"KEPT=1", name + "=first", "OTHER=2", name + "=", name, name + "=" + name + "=x"}
			got := Build("", environ)
			if want := []string{"KEPT=1", "OTHER=2"}; !slices.Equal(got.Env, want) {
				t.Errorf("env = %q, want %q", got.Env, want)
			}
		})
	}
}

func TestBuildKeepsEverythingElseInOrder(t *testing.T) {
	environ := []string{
		"PATH=/bin", "HOME=/home/x", "GH_TOKEN_X=1", "XGH_TOKEN=1", "gh_token=lower", "ZIZMOR_RENDER_LINKS=1",
		"ZIZMOR_SHOW_AUDIT_URLS=1", "RUST_LOG=debug", "NO_COLOR=1", "XDG_CACHE_HOME=/c", "GIT_CONFIG_COUNT=0", "ZIZMOR=1", "ZIZMOR_OFFLINE_X=1",
	}
	if got := Build("", environ); !slices.Equal(got.Env, environ) {
		t.Errorf("env = %q, want it unchanged: %q", got.Env, environ)
	}
}

func TestBuildSetsTheToken(t *testing.T) {
	environ := []string{"A=1", "GH_TOKEN=old", "GITHUB_TOKEN=older", "B=2"}
	for _, tc := range []struct {
		name  string
		token string
		want  []string
	}{
		{name: "a token replaces the ones in the environment, last", token: "new", want: []string{"A=1", "B=2", "GH_TOKEN=new"}},
		{name: "no token leaves neither", token: "", want: []string{"A=1", "B=2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Build(tc.token, environ); !slices.Equal(got.Env, tc.want) {
				t.Errorf("env = %q, want %q", got.Env, tc.want)
			}
		})
	}
}

func TestBuildDoesNotChangeItsInput(t *testing.T) {
	environ := []string{"A=1", "GH_TOKEN=old", "B=2"}
	before := slices.Clone(environ)
	Build("new", environ)
	if !slices.Equal(environ, before) {
		t.Errorf("the environment given was changed: %q", environ)
	}
}

func TestToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "neither set", want: ""},
		{name: "both empty", env: map[string]string{"GH_TOKEN": "", "GITHUB_TOKEN": ""}, want: ""},
		{name: "GH_TOKEN only", env: map[string]string{"GH_TOKEN": "first"}, want: "first"},
		{name: "GITHUB_TOKEN alone is passed on", env: map[string]string{"GITHUB_TOKEN": "second"}, want: "second"},
		{name: "both set: GH_TOKEN wins", env: map[string]string{"GH_TOKEN": "first", "GITHUB_TOKEN": "second"}, want: "first"},
		{name: "GH_TOKEN empty: GITHUB_TOKEN counts", env: map[string]string{"GH_TOKEN": "", "GITHUB_TOKEN": "second"}, want: "second"},
		{name: "GITHUB_TOKEN empty: GH_TOKEN counts", env: map[string]string{"GH_TOKEN": "first", "GITHUB_TOKEN": ""}, want: "first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Token(func(name string) string { return tc.env[name] }); got != tc.want {
				t.Errorf("Token = %q, want %q", got, tc.want)
			}
		})
	}
}
