package main

import "testing"

func TestParseCronLine(t *testing.T) {
	cases := []struct{ in, app, ok, after, vpn string }{
		{"2026-10-29 15:00:06 +0700 app=line ok=no before=NotificationShade after=NotificationShade vpn=up pb=down", "line", "no", "NotificationShade", "up"},
		{"2026-10-29 15:15:06 +0700 app=messenger ok=yes before=none after=com.facebook.orca/com.facebook.messenger.neue.MainActivity vpn=up pb=up", "messenger", "yes", "com.facebook.orca/com.facebook.messenger.neue.MainActivity", "up"},
		{"2026-10-29 15:30:06 +0700 app=- ok=skip before=com.android.chrome note=phone-in-use vpn=down pb=down", "-", "skip", "", "down"},
		{"2026-10-29 15:00:06 line: NotificationShade", "line", "no", "NotificationShade", ""},
		{"2026-10-25 08:31:02 line: jp.naver.line.android/jp.naver.line.android.activity.main.MainActivity", "line", "yes", "jp.naver.line.android/jp.naver.line.android.activity.main.MainActivity", ""},
		{"2026-10-25 08:46:13 messenger: inbox (Back x1)", "messenger", "yes", "inbox (Back x1)", ""},
		{"2026-10-25 08:45:14 messenger: NOT on inbox after Back x3", "messenger", "no", "NOT on inbox after Back x3", ""},
		{"2026-10-25 09:00:00 skip: phone in use (mCurrentFocus=Window{1 u0 com.android.chrome})", "-", "skip", "", ""},
	}
	for _, c := range cases {
		r, ok := parseCronLine(c.in)
		if !ok || r.App != c.app || r.Ok != c.ok || r.After != c.after || r.VPN != c.vpn {
			t.Errorf("%q -> %+v (parsed=%v)", c.in, r, ok)
		}
	}
	if r, _ := parseCronLine("2026-10-29 15:00:06 line: NotificationShade"); r.At != "2026-10-29T15:00:06+07:00" {
		t.Errorf("legacy zone: %s", r.At)
	}
	if r, _ := parseCronLine("2026-10-25 09:00:00 skip: phone in use (mCurrentFocus=Window{1a2b u0 com.android.chrome/org.chromium.chrome.browser.ChromeTabbedActivity})"); r.Before != "com.android.chrome/org.chromium.chrome.browser.ChromeTabbedActivity" {
		t.Errorf("legacy skip before: %q", r.Before)
	}
	if _, ok := parseCronLine("crond: USER root pid 1 cmd x"); ok {
		t.Error("junk line parsed")
	}
}
