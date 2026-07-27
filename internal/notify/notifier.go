// Package notify provides desktop notification support.
package notify

import (
	"regexp"
	"strings"

	"github.com/gammons/slk/internal/usergroups"
	"github.com/gen2brain/beeep"
)

// Notifier sends OS-level desktop notifications.
type Notifier struct {
	enabled bool
}

// New creates a Notifier. If enabled is false, Notify is a no-op.
func New(enabled bool) *Notifier {
	return &Notifier{enabled: enabled}
}

// Notify sends a desktop notification with the given title and body.
// Returns nil if notifications are disabled.
func (n *Notifier) Notify(title, body string) error {
	if !n.enabled {
		return nil
	}
	return beeep.Notify(title, body, "")
}

// NotifyContext holds the state needed to evaluate notification triggers.
type NotifyContext struct {
	CurrentUserID   string
	ActiveChannelID string
	IsActiveWS      bool
	OnMention       bool
	OnDM            bool
	OnKeyword       []string
	IsDND           bool // when true, ShouldNotify always returns false
}

// ShouldNotify returns true if a message should trigger a desktop notification.
func ShouldNotify(ctx NotifyContext, channelID, userID, text, channelType string) bool {
	// Never notify for own messages
	if userID == ctx.CurrentUserID {
		return false
	}

	// Suppress entirely while DND/snoozed.
	if ctx.IsDND {
		return false
	}

	// Suppress if viewing this channel on the active workspace
	if ctx.IsActiveWS && channelID == ctx.ActiveChannelID {
		return false
	}

	// Check DM trigger
	if ctx.OnDM && (channelType == "dm" || channelType == "group_dm") {
		return true
	}

	// Check mention trigger
	if ctx.OnMention && (strings.Contains(text, "<@"+ctx.CurrentUserID+">") ||
		strings.Contains(text, "<!here>") ||
		strings.Contains(text, "<!channel>") ||
		strings.Contains(text, "<!everyone>")) {
		return true
	}

	// Check keyword triggers
	if len(ctx.OnKeyword) > 0 {
		lower := strings.ToLower(text)
		for _, kw := range ctx.OnKeyword {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return true
			}
		}
	}

	return false
}

var (
	userMentionRe    = regexp.MustCompile(`<@([A-Z0-9]+)>`)
	channelMentionRe = regexp.MustCompile(`<#[A-Z0-9]+\|([^>]+)>`)
	// Group 1 is the ID; group 2 the optional embedded label. Labeled
	// forms are normalized to "@label"; bare forms resolve through the
	// caller's workspace-scoped usergroup map.
	subteamMentionRe = regexp.MustCompile(`<!subteam\^([A-Z0-9]+)(?:\|([^>]+))?>`)
	broadcastRe      = regexp.MustCompile(`<!(here|channel|everyone)>`)
	// Match both http(s) URLs and mailto: addresses; Slack
	// auto-linkifies typed emails into <mailto:X|X>. Bare-link
	// substitution keeps the URL as-is for http(s) but strips the
	// mailto: prefix so the notification body reads as just the
	// address — see StripSlackMarkup below.
	linkWithLabelRe = regexp.MustCompile(`<((?:https?://|mailto:)[^|>]+)\|([^>]+)>`)
	linkBareRe      = regexp.MustCompile(`<((?:https?://|mailto:)[^>]+)>`)
)

// StripSlackMarkup converts Slack-formatted text to plain text suitable for
// OS notification bodies. User mentions are resolved against userNames; if
// a user ID is missing from the map (or the map is nil) the raw user ID is
// used as a fallback. Output is truncated to 100 characters with "..." suffix.
func StripSlackMarkup(text string, userNames map[string]string) string {
	return StripSlackMarkupWithUserGroups(text, userNames, nil)
}

// StripSlackMarkupWithUserGroups is StripSlackMarkup with a workspace-scoped
// Slack usergroup map for resolving bare <!subteam^SID> tokens.
func StripSlackMarkupWithUserGroups(text string, userNames map[string]string, userGroups map[string]string) string {
	text = channelMentionRe.ReplaceAllString(text, "#$1")
	text = linkWithLabelRe.ReplaceAllString(text, "$2")
	// Bare links: drop the mailto: scheme so notification bodies read
	// as just the address; http(s) URLs are kept whole.
	text = linkBareRe.ReplaceAllStringFunc(text, func(match string) string {
		url := linkBareRe.FindStringSubmatch(match)[1]
		return strings.TrimPrefix(url, "mailto:")
	})
	text = subteamMentionRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := subteamMentionRe.FindStringSubmatch(match)
		return usergroups.Display(userGroups, groups[1], groups[2])
	})
	text = broadcastRe.ReplaceAllString(text, "@$1")
	text = userMentionRe.ReplaceAllStringFunc(text, func(match string) string {
		userID := userMentionRe.FindStringSubmatch(match)[1]
		if name, ok := userNames[userID]; ok {
			return "@" + name
		}
		return "@" + userID
	})
	text = strings.ReplaceAll(text, "*", "")
	text = strings.ReplaceAll(text, "_", "")
	text = strings.ReplaceAll(text, "~", "")
	text = strings.ReplaceAll(text, "`", "")

	if len(text) > 100 {
		text = text[:100] + "..."
	}

	return text
}
