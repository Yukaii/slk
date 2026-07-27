package messages

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Broadcast mentions (<!here>, <!channel>, <!everyone>, optionally
// labeled <!channel|@channel>) must render as @here/@channel/@everyone
// in the styled message view and the CommonMark export — the snippet
// flattener and notifications already handle them; the raw wire token
// must never leak into the message pane.

func TestRenderBroadcastMentions(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"<!channel> Team support this week", "@channel Team support this week"},
		{"heads up <!here> now", "heads up @here now"},
		{"<!everyone> announcement", "@everyone announcement"},
		{"labeled <!channel|@channel> form", "labeled @channel form"},
	}
	for _, tc := range cases {
		out := ansi.Strip(RenderSlackMarkdown(tc.in, nil, nil))
		if !strings.Contains(out, tc.want) {
			t.Errorf("RenderSlackMarkdown(%q) = %q, want to contain %q", tc.in, out, tc.want)
		}
		if strings.Contains(out, "<!") {
			t.Errorf("raw broadcast token leaked: %q", out)
		}
	}
}

func TestCommonMarkBroadcastMentions(t *testing.T) {
	out := SlackMrkdwnToCommonMark("<!channel> support this week by <!here>", nil, nil)
	if out != "@channel support this week by @here" {
		t.Errorf("SlackMrkdwnToCommonMark = %q, want %q", out, "@channel support this week by @here")
	}
}
