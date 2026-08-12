package sysinfo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNormalizeVersion 版本号规范化
func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v0.2.0", "0.2.0"},
		{"V1.2.3", "1.2.3"},
		{"0.2.0", "0.2.0"},
		{"v1.0", "1.0"},
		{"1.2.3-beta.1", "1.2.3"},
		{"v0.1.0 (beta)", "0.1.0"},
	}
	for _, c := range cases {
		if got := normalizeVersion(c.in); got != c.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCompareVersion 点分数字版本比较
func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},   // 不按字符串比较
		{"0.1", "0.1.5", -1},     // 位数不同
		{"1.2.3", "1.2.3.4", -1}, // 多一段视为更大
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCheckUpdateStates 版本检查状态语义：outdated / up-to-date / ahead / 404 / 网络错误
func TestCheckUpdateStates(t *testing.T) {
	saveVer, saveAPI := AppVersion, releaseAPI
	defer func() {
		AppVersion, releaseAPI = saveVer, saveAPI
	}()

	AppVersion = "0.2.0"

	tests := []struct {
		name       string
		tag        string // GitHub tag_name；空=不设置
		status     int    // GitHub 返回状态码
		wantState  string
		wantUpdate bool
		wantErr    bool
	}{
		{"线上有新版本", "v0.3.0", http.StatusOK, "outdated", true, false},
		{"版本相同", "v0.2.0", http.StatusOK, "up-to-date", false, false},
		{"本地领先", "v0.1.0", http.StatusOK, "ahead", false, false}, // 关键：本地大于在线，非异常
		{"仓库无 release", "", http.StatusNotFound, "up-to-date", false, false},
		{"版本号无法解析", "unknown", http.StatusOK, "up-to-date", false, false},
		{"GitHub 异常状态码", "", http.StatusForbidden, "error", false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK && tc.tag != "" {
					_, _ = w.Write([]byte(`{"tag_name":"` + tc.tag + `","html_url":"https://github.com/jafox1024/DriveWise/releases/tag/` + tc.tag + `"}`))
				}
			}))
			defer ts.Close()
			releaseAPI = ts.URL

			svc := &SysService{}
			info := svc.CheckUpdate()
			if info.State != tc.wantState {
				t.Errorf("State = %q, want %q", info.State, tc.wantState)
			}
			if info.HasUpdate != tc.wantUpdate {
				t.Errorf("HasUpdate = %v, want %v", info.HasUpdate, tc.wantUpdate)
			}
			if (info.Error != "") != tc.wantErr {
				t.Errorf("Error = %q, wantErr=%v", info.Error, tc.wantErr)
			}
		})
	}
}
