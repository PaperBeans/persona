package repository

import "testing"

func TestParseRemote(t *testing.T) {
	for _, tc := range []struct{ remote, provider, instance, path string }{
		{"https://GitHub.com/Example/Project.git/", "", "https://github.com", "example/project"},
		{"git@code.example.com:Example/Project.git", "github", "https://code.example.com", "example/project"},
		{"ssh://git@code.example.com:22/example/project.git", "github", "https://code.example.com", "example/project"},
		{"https://code.example.com:8443/team/project.git", "github", "https://code.example.com:8443", "team/project"},
		{"git@code.example.com:group/subgroup/project.git", "gitlab", "https://code.example.com", "group/subgroup/project"},
		{"https://gitlab.com/group/subgroup/project", "", "https://gitlab.com", "group/subgroup/project"},
		{"ssh://git@code.example.com/group/sub/project", "gitlab", "https://code.example.com", "group/sub/project"},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			got, err := Parse(tc.remote, tc.provider)
			if err != nil || got.InstanceURL != tc.instance || got.Path != tc.path || got.CanonicalURL != tc.instance+"/"+tc.path {
				t.Fatalf("Parse: %+v, %v", got, err)
			}
		})
	}
}

func TestRejectUnsafeOrAmbiguousRemote(t *testing.T) {
	for _, remote := range []string{
		"http://github.com/example/project", "file:///etc/passwd", "git://github.com/example/project",
		"https://user:password@code.example.com/example/project", "https://token@code.example.com/example/project",
		"https://github.com/example/project?token=secret", "https://github.com/example/project#fragment",
		"ssh://user:password@code.example.com/example/project", "ssh://other@code.example.com/example/project",
		"git@code.example.com:example/../project", "https://github.com/example/%2e%2e/project",
		"https://github.com/example/project/extra", "https://github.com/example//project",
		"https://github.com./example/project", "https://github.com:80/example/project",
		"ssh://git@code.example.com:2222/example/project", "https://github.com/example/project\n",
		"https://github.com/example/project.git.git", "https://[fe80::1%25eth0]/example/project",
	} {
		if got, err := Parse(remote, "github"); err == nil {
			t.Errorf("accepted %q: %+v", remote, got)
		}
	}
	if _, err := Parse("https://code.example.com/group/project", ""); err == nil {
		t.Fatal("a custom instance needs an explicit provider, not a guessed API")
	}
	if _, err := Parse("https://github.com/example/project", "gitlab"); err == nil {
		t.Fatal("a known provider cannot be relabeled")
	}
}
