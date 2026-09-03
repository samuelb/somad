//go:build darwin

package notify

import "testing"

func TestAppleScriptQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "Dayvan Cowboy",
			want:  `"Dayvan Cowboy"`,
		},
		{
			name:  "embedded double quote",
			input: `Track "Remix"`,
			want:  `"Track \"Remix\""`,
		},
		{
			name:  "embedded backslash",
			input: `C:\Music`,
			want:  `"C:\\Music"`,
		},
		{
			name:  "backslash before a quote is escaped independently",
			input: `\"`,
			want:  `"\\\""`,
		},
		{
			name:  "attempted script injection",
			input: `" & (do shell script "rm -rf ~") & "`,
			want:  `"\" & (do shell script \"rm -rf ~\") & \""`,
		},
		{
			name:  "newline is flattened to a space",
			input: "Line one\nLine two",
			want:  `"Line one Line two"`,
		},
		{
			name:  "CRLF is flattened to a single space",
			input: "Line one\r\nLine two",
			want:  `"Line one Line two"`,
		},
		{
			name:  "empty string",
			input: "",
			want:  `""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appleScriptQuote(tt.input)
			if got != tt.want {
				t.Errorf("appleScriptQuote(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}
