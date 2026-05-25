package handlers

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCampaignFailures_DateValidation(t *testing.T) {
	h := &CampaignFailuresHandler{}

	cases := []struct {
		name string
		url  string
		want int
	}{
		{"valid_yesterday", "/?date=" + time.Now().AddDate(0, 0, -1).Format("2006-01-02"), 200},
		{"future", "/?date=" + time.Now().AddDate(0, 0, 5).Format("2006-01-02"), 400},
		{"too_old", "/?date=" + time.Now().AddDate(0, 0, -100).Format("2006-01-02"), 400},
		{"malformed", "/?date=garbage", 400},
		{"default_empty", "/", 200},
		{"mode_historical", "/?mode=historical", 200},
		{"mode_invalid", "/?mode=garbage", 400},
		{"mode_and_date_conflict", "/?mode=historical&date=2026-05-01", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.url, nil)
			rr := httptest.NewRecorder()
			h.GetList(rr, req)
			if tc.want != rr.Code {
				t.Errorf("%s: want %d, got %d body=%s", tc.name, tc.want, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCampaignFailures_HistoricalPaginationClamps(t *testing.T) {
	h := &CampaignFailuresHandler{}
	// Repo nil → empty 200. We only confirm parsing accepts edge values.
	for _, qs := range []string{
		"mode=historical&page=0",
		"mode=historical&page=999",
		"mode=historical&page_size=-5",
		"mode=historical&page_size=99999",
		"mode=historical&page=abc&page_size=def",
	} {
		req := httptest.NewRequest("GET", "/?"+qs, nil)
		rr := httptest.NewRecorder()
		h.GetList(rr, req)
		if rr.Code != 200 {
			t.Errorf("%q: want 200, got %d", qs, rr.Code)
		}
	}
}
