package selfupdate

import "testing"

func TestParseVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    Version
		wantErr bool
	}{
		{name: "release tag", input: "v0.2.2", want: Version{Patch: 2, Minor: 2}},
		{name: "goreleaser version", input: "0.2.2", want: Version{Minor: 2, Patch: 2}},
		{name: "pre-release", input: "v1.0.0-rc.1", want: Version{Major: 1, Pre: "rc.1"}},
		{name: "git describe", input: "v0.2.2-7-gabc1234", want: Version{Minor: 2, Patch: 2, Commits: 7}},
		{name: "git describe dirty", input: "v0.2.2-7-gabc1234-dirty", want: Version{Minor: 2, Patch: 2, Commits: 7, Dirty: true}},
		{name: "dirty tag", input: "v0.2.2-dirty", want: Version{Minor: 2, Patch: 2, Dirty: true}},
		{name: "describe after pre-release", input: "v1.0.0-rc.1-3-gdeadbeef", want: Version{Major: 1, Pre: "rc.1", Commits: 3}},
		{
			name:  "go pseudo-version",
			input: "v0.2.3-0.20261022093000-abcdef123456+dirty",
			want:  Version{Minor: 2, Patch: 3, Pre: "0.20261022093000-abcdef123456", Dirty: true},
		},
		{name: "dev", input: "dev", wantErr: true},
		{name: "commit only", input: "abc1234", wantErr: true},
		{name: "two parts", input: "v0.2", wantErr: true},
		{name: "leading zero", input: "v01.2.3", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseVersion(%q) error = %v, want error %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseVersion(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestVersion_Tag(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{"0.2.2": "v0.2.2", "v1.0.0-rc.1-3-gdeadbeef-dirty": "v1.0.0-rc.1"} {
		v, err := ParseVersion(input)
		if err != nil || v.Tag() != want {
			t.Errorf("ParseVersion(%q).Tag() = %q, %v, want %q", input, v.Tag(), err, want)
		}
	}
}

func TestVersion_Compare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{name: "equal with and without v", a: "0.2.2", b: "v0.2.2", want: 0},
		{name: "patch", a: "v0.2.1", b: "v0.2.2", want: -1},
		{name: "minor beats patch", a: "v0.3.0", b: "v0.2.9", want: 1},
		{name: "major", a: "v1.0.0", b: "v0.99.99", want: 1},
		{name: "numeric not lexical", a: "v0.10.0", b: "v0.9.0", want: 1},
		{name: "pre-release before release", a: "v1.0.0-rc.1", b: "v1.0.0", want: -1},
		{name: "pre-release numeric", a: "v1.0.0-rc.2", b: "v1.0.0-rc.10", want: -1},
		{name: "pre-release numeric before alphanumeric", a: "v1.0.0-1", b: "v1.0.0-alpha", want: -1},
		{name: "pre-release longer after prefix", a: "v1.0.0-alpha", b: "v1.0.0-alpha.1", want: -1},
		{name: "describe after tag", a: "v0.2.2-7-gabc1234", b: "v0.2.2", want: 1},
		{name: "describe before next release", a: "v0.2.2-7-gabc1234", b: "v0.2.3", want: -1},
		{name: "dirty after clean", a: "v0.2.2-dirty", b: "v0.2.2", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, err := ParseVersion(tt.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := ParseVersion(tt.b)
			if err != nil {
				t.Fatal(err)
			}
			if got := a.Compare(b); got != tt.want {
				t.Errorf("%s.Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
			if got := b.Compare(a); got != -tt.want {
				t.Errorf("%s.Compare(%s) = %d, want %d", tt.b, tt.a, got, -tt.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		current, target string
		want            Change
	}{
		{name: "same", current: "0.2.2", target: "v0.2.2", want: Same},
		{name: "upgrade", current: "0.2.0", target: "v0.2.2", want: Upgrade},
		{name: "downgrade", current: "0.2.2", target: "v0.1.0", want: Downgrade},
		{name: "dev build of the tag is newer", current: "v0.2.2-7-gabc1234", target: "v0.2.2", want: Downgrade},
		{name: "dev build before the next release", current: "v0.2.2-7-gabc1234-dirty", target: "v0.3.0", want: Upgrade},
		{name: "unversioned build", current: "dev", target: "v0.2.2", want: Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(tt.current, tt.target); got != tt.want {
				t.Errorf("Classify(%q, %q) = %d, want %d", tt.current, tt.target, got, tt.want)
			}
		})
	}
}
