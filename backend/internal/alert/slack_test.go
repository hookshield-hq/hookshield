package alert

import "testing"

func TestSlackEscape(t *testing.T) {
	if got := slackEscape("<https://evil.example|Reset your password> & <!channel>"); got != "&lt;https://evil.example|Reset your password&gt; &amp; &lt;!channel&gt;" {
		t.Fatalf("got %q", got)
	}
}
