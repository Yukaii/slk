// Package slackdesktop reads the Slack desktop application's on-disk state:
// the session `d` cookie (from the app's encrypted Cookies sqlite DB) and the
// list of signed-in workspaces (from storage/root-state.json).
//
// The cookie-decryption logic is adapted from github.com/rneatherway/slack
// (MIT License, Copyright (c) rneatherway). Original notice retained.
package slackdesktop
