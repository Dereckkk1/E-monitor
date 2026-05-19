package handlers

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestStationFailures_DateValidation(t *testing.T) {
	h := &StationFailuresHandler{}

	cases := []struct {
		name string
		date string
		want int
	}{
		{"valid_yesterday", time.Now().AddDate(0, 0, -1).Format("2006-01-02"), 200},
		{"future", time.Now().AddDate(0, 0, 2).Format("2006-01-02"), 400},
		{"too_old", time.Now().AddDate(0, 0, -100).Format("2006-01-02"), 400},
		{"malformed", "not-a-date", 400},
		{"default_empty", "", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/?date=" + tc.date
			if tc.date == "" {
				url = "/"
			}
			req := httptest.NewRequest("GET", url, nil)
			rr := httptest.NewRecorder()
			h.Get(rr, req)
			if tc.want == 400 && rr.Code != 400 {
				t.Errorf("date %q: want 400, got %d (body=%s)", tc.date, rr.Code, rr.Body.String())
			}
			if tc.want == 200 && rr.Code == 400 {
				t.Errorf("date %q: should not be 400, got 400 body=%s", tc.date, rr.Body.String())
			}
		})
	}
}

func TestStationFailures_MinDownSecondsClamp(t *testing.T) {
	h := &StationFailuresHandler{}
	// Repo nil => returns empty 200 regardless. We just confirm parsing
	// doesn't panic on out-of-range values.
	for _, v := range []string{"-1", "9999", "abc", "0", "60", "3600"} {
		req := httptest.NewRequest("GET", "/?min_down_seconds="+v, nil)
		rr := httptest.NewRecorder()
		h.Get(rr, req)
		if rr.Code != 200 {
			t.Errorf("min_down_seconds=%q: want 200, got %d", v, rr.Code)
		}
	}
}
