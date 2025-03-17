package dman

import "testing"

func TestTransformPath(t *testing.T) {
	base := RepoDir()
	home := "/Users/user"

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "simple dotfile on mac",
			path: "/Users/user/.zshrc",
			want: base + "/dot_zshrc",
		},
		{
			name: "dotfile in sub dir on mac",
			path: "/Users/user/.config/nvim/init.lua",
			want: base + "/dot_config/nvim/init.lua",
		},
		{
			name: "dotfile in sub dir on mac 2",
			path: "/Users/user/.config/.config",
			want: base + "/dot_config/.config",
		},
		{
			name:    "no dotfile",
			path:    "/Users/user/Downloads",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := transformPath(home, base, tc.path)
			if err != nil && !tc.wantErr {
				t.Fail()
			}
			if tc.wantErr && err == nil {
				t.Error("no error")
				t.Fail()
			} else if tc.want != got {
				t.Errorf("want: %s; got: %s", tc.want, got)
				t.Fail()
			}
		})
	}
}
