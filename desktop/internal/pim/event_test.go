package pim

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"PT1H":    time.Hour,
		"P1D":     24 * time.Hour,
		"P1W":     7 * 24 * time.Hour,
		"PT1H30M": 90 * time.Minute,
		"P1DT2H":  26 * time.Hour,
		"PT0S":    0,
		"PT15M":   15 * time.Minute,
		"PT1.5S":  1500 * time.Millisecond,
	} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseDuration("1H"); err == nil {
		t.Error("expected error for malformed duration")
	}
	if FormatDuration(26*time.Hour+30*time.Minute) != "P1DT2H30M" {
		t.Errorf("FormatDuration = %s", FormatDuration(26*time.Hour+30*time.Minute))
	}
}

func TestExpandWeeklyWithOverrides(t *testing.T) {
	raw := []byte(`{
		"id": "e1", "title": "周会", "start": "2026-09-07T10:00:00", "timeZone": "Asia/Shanghai",
		"duration": "PT1H", "calendarIds": {"b": true},
		"recurrenceRules": [{"@type": "RecurrenceRule", "frequency": "weekly", "byDay": [{"day": "mo"}], "count": 4}],
		"recurrenceOverrides": {
			"2026-09-14T10:00:00": {"excluded": true},
			"2026-09-21T10:00:00": {"title": "周会(改期)", "start": "2026-09-22T15:00:00"}
		}
	}`)
	e, err := ParseEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	sh, _ := time.LoadLocation("Asia/Shanghai")
	occ, err := e.Expand(time.Date(2026, 9, 1, 0, 0, 0, 0, sh), time.Date(2026, 10, 1, 0, 0, 0, 0, sh))
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 3 {
		t.Fatalf("got %d occurrences, want 3: %+v", len(occ), occ)
	}
	if occ[1].Title != "周会(改期)" || occ[1].Start.In(sh).Hour() != 15 || occ[1].RecurrenceID != "2026-09-21T10:00:00" {
		t.Errorf("override not applied: %+v", occ[1])
	}
	if !occ[2].Start.Equal(time.Date(2026, 9, 28, 10, 0, 0, 0, sh)) {
		t.Errorf("last occurrence = %v", occ[2].Start)
	}

	start, end, err := e.Span()
	if err != nil || end == nil {
		t.Fatalf("span: %v %v", end, err)
	}
	if start != time.Date(2026, 9, 7, 10, 0, 0, 0, sh).Unix() || *end < time.Date(2026, 9, 28, 11, 0, 0, 0, sh).Unix() {
		t.Errorf("span = %d..%d", start, *end)
	}
}

func TestExpandAllDayAndOpenEnded(t *testing.T) {
	e, _ := ParseEvent([]byte(`{"id":"h","title":"立夏","start":"2024-05-05T00:00:00","showWithoutTime":true}`))
	occ, _ := e.Expand(time.Date(2024, 5, 1, 0, 0, 0, 0, time.Local), time.Date(2024, 6, 1, 0, 0, 0, 0, time.Local))
	if len(occ) != 1 || !occ[0].AllDay || occ[0].End.Sub(occ[0].Start) != 24*time.Hour {
		t.Fatalf("all-day: %+v", occ)
	}

	daily, _ := ParseEvent([]byte(`{"id":"d","title":"站会","start":"2026-01-01T09:00:00","timeZone":"UTC","duration":"PT15M",
		"recurrenceRules":[{"frequency":"daily"}]}`))
	if _, end, _ := daily.Span(); end != nil {
		t.Error("open-ended rule should have nil end")
	}
	occ, _ = daily.Expand(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC))
	if len(occ) != 7 {
		t.Errorf("daily over a week = %d", len(occ))
	}
}

// Stalwart 返回单数的 recurrenceRule。
func TestExpandSingularRule(t *testing.T) {
	e, _ := ParseEvent([]byte(`{"id":"s","title":"x","start":"2030-01-01T10:00:00","timeZone":"Asia/Shanghai","duration":"PT1H",
		"recurrenceRule":{"frequency":"weekly","byDay":[{"day":"mo"}],"until":"2030-03-01T00:00:00"},
		"recurrenceOverrides":{"2030-01-07T10:00:00":{"excluded":true}}}`))
	sh, _ := time.LoadLocation("Asia/Shanghai")
	occ, _ := e.Expand(time.Date(2030, 1, 1, 0, 0, 0, 0, sh), time.Date(2030, 2, 1, 0, 0, 0, 0, sh))
	// 1/1 本身(周二) + 1/14, 1/21, 1/28 (1/7 被排除)
	if len(occ) != 4 {
		t.Fatalf("got %d: %+v", len(occ), occ)
	}
	if _, end, _ := e.Span(); end == nil {
		t.Error("until rule should be bounded")
	}
}
