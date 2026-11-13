package i18n

import "testing"

func TestTerminal(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want Lang
	}{
		{"nothing set", nil, En},
		{"LANG Chinese", map[string]string{"LANG": "zh_CN.UTF-8"}, ZhHans},
		{"LANG Traditional Chinese", map[string]string{"LANG": "zh_TW.Big5"}, ZhHans},
		{"LANG Japanese", map[string]string{"LANG": "ja_JP.UTF-8"}, Ja},
		{"LANG English", map[string]string{"LANG": "en_GB.UTF-8"}, En},
		{"LANG other language", map[string]string{"LANG": "fr_FR.UTF-8"}, En},
		{"LANG with modifier", map[string]string{"LANG": "ja_JP.eucJP@euro"}, Ja},
		{"C", map[string]string{"LANG": "C"}, En},
		{"C.UTF-8", map[string]string{"LANG": "C.UTF-8"}, En},
		{"POSIX", map[string]string{"LANG": "POSIX"}, En},
		{"LC_ALL over LANG", map[string]string{"LC_ALL": "ja_JP.UTF-8", "LANG": "zh_CN.UTF-8"}, Ja},
		{"LC_ALL C over LANG", map[string]string{"LC_ALL": "C", "LANG": "zh_CN.UTF-8"}, En},
		{"LC_MESSAGES over LANG", map[string]string{"LC_MESSAGES": "zh_CN.UTF-8", "LANG": "en_US.UTF-8"}, ZhHans},
		{"LC_ALL over LC_MESSAGES", map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "zh_CN.UTF-8"}, En},
		{"PERSONA_LANG over the locale", map[string]string{"PERSONA_LANG": "zh-Hans", "LC_ALL": "en_US.UTF-8"}, ZhHans},
		{"PERSONA_LANG as a tag", map[string]string{"PERSONA_LANG": "ja-JP", "LANG": "en_US.UTF-8"}, Ja},
		{"PERSONA_LANG unknown", map[string]string{"PERSONA_LANG": "ko", "LANG": "zh_CN.UTF-8"}, En},
		{"blank is not set", map[string]string{"PERSONA_LANG": " ", "LC_ALL": "", "LANG": "ja_JP.UTF-8"}, Ja},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range TerminalEnv {
				t.Setenv(name, tc.env[name])
			}
			if got := Terminal(); got != tc.want {
				t.Fatalf("Terminal() = %q, want %q", got, tc.want)
			}
		})
	}
}
