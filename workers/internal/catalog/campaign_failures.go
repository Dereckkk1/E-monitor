package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IsBonified reports whether enough non-strict-in-slot plays exist to cover
// the remaining deficit. Pure function — used by all 3 endpoints (daily,
// historical, drill-in) to keep the rule consistent.
//
// Definition mirrors the external "Relatório Campanha" semantics:
//   extras    = out_slot + out_date + bonus aggregated across campaign period
//   deficit   = sum of daily_play_summary.deficit across campaign period
//   bonified  = (extras >= deficit) AND extras > 0 AND deficit > 0
//
// Zero deficit means nothing failed → never bonified (nothing to compensate).
func IsBonified(deficit, extras int) bool {
	if deficit <= 0 || extras <= 0 {
		return false
	}
	return extras >= deficit
}

// CampaignFailures repo — see ListForDate / ListHistorical / Get below.
type CampaignFailures struct {
	pool *pgxpool.Pool
}

func NewCampaignFailures(pool *pgxpool.Pool) *CampaignFailures {
	return &CampaignFailures{pool: pool}
}

// ─── Output types ────────────────────────────────────────────────

type CampaignInfo struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	ClientID      uuid.UUID `json:"client_id"`
	ClientName    string    `json:"client_name"`
	ClientLogoURL string    `json:"client_logo_url"`
	StartDate     string    `json:"start_date"` // YYYY-MM-DD
	EndDate       string    `json:"end_date"`
	Status        string    `json:"status"`
}

type StationFailureInfo struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Dial    string    `json:"dial"`
	City    string    `json:"city"`
	LogoURL string    `json:"logo_url"`
}

type CampaignFailureStation struct {
	Station           StationFailureInfo `json:"station"`
	Programmed        int                `json:"programmed"`
	Identified        int                `json:"identified"`
	Deficit           int                `json:"deficit"`
	Extras            int                `json:"extras"`
	IsBonified        bool               `json:"is_bonified"`
	FailureDaysOnDate []string           `json:"failure_days_on_date,omitempty"` // only in modo dia
	FailureDays       []string           `json:"failure_days,omitempty"`         // only in drill-in
}

type CampaignDailyFailure struct {
	Campaign CampaignInfo             `json:"campaign"`
	Stations []CampaignFailureStation `json:"stations"`
}

type CampaignDailySummary struct {
	Campaigns    int `json:"campaigns"`
	Stations     int `json:"stations"`
	TotalDeficit int `json:"total_deficit"`
}

type DailyResult struct {
	Mode      string                 `json:"mode"` // "by_date"
	Date      string                 `json:"date"`
	Summary   CampaignDailySummary   `json:"summary"`
	Campaigns []CampaignDailyFailure `json:"campaigns"`
}

type CampaignHistoricalRow struct {
	Campaign            CampaignInfo `json:"campaign"`
	StationsWithFailure int          `json:"stations_with_failure"`
	TotalFailureDays    int          `json:"total_failure_days"`
	TotalDeficit        int          `json:"total_deficit"`
	IsFullyBonified     bool         `json:"is_fully_bonified"`
}

type HistoricalSummary struct {
	Campaigns        int `json:"campaigns"`
	TotalFailureDays int `json:"total_failure_days"`
}

type HistoricalResult struct {
	Mode      string                  `json:"mode"` // "historical"
	Summary   HistoricalSummary       `json:"summary"`
	Campaigns []CampaignHistoricalRow `json:"campaigns"`
	Page      int                     `json:"page"`
	PageSize  int                     `json:"page_size"`
	Total     int                     `json:"total"`
}

type DetailSummary struct {
	StationsWithFailure int `json:"stations_with_failure"`
	TotalFailureDays    int `json:"total_failure_days"`
	TotalDeficit        int `json:"total_deficit"`
}

type DetailResult struct {
	Campaign CampaignInfo             `json:"campaign"`
	Summary  DetailSummary            `json:"summary"`
	Stations []CampaignFailureStation `json:"stations"`
}

// ─── Helpers ─────────────────────────────────────────────────────

// makeDial joins a pre-formatted frequency string with the band into
// "102.7 FM" / "1080 AM". Both args may be empty.
func makeDial(freq, band string) string {
	if freq != "" && band != "" {
		return freq + " " + band
	}
	if freq != "" {
		return freq
	}
	return band
}

// dateOnly trims a time.Time to YYYY-MM-DD.
func dateOnly(t time.Time) string { return t.Format("2006-01-02") }

// _ used to silence unused imports during stub phase.
var _ = context.Background
var _ = fmt.Sprintf
