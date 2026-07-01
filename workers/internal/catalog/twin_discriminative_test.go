package catalog

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/audit"
	"radiocheck/internal/similarity"
)

func TestFilterTwinsByDuration(t *testing.T) {
	own := 30.0
	cands := []similarity.SimilarMaterial{
		{ID: uuid.New(), Score: 0.9, DurationSeconds: 30.0},  // keep (exact)
		{ID: uuid.New(), Score: 0.8, DurationSeconds: 30.7},  // keep (within 1s)
		{ID: uuid.New(), Score: 0.95, DurationSeconds: 15.0}, // drop (15s vs 30s)
		{ID: uuid.New(), Score: 0.7, DurationSeconds: 31.2},  // drop (>1s)
	}
	got := filterTwinsByDuration(cands, own)
	if len(got) != 2 {
		t.Fatalf("kept %d, want 2: %+v", len(got), got)
	}
}

func TestComplementRanges(t *testing.T) {
	fr := func(lo, hi int32) audit.FrameRange { return audit.FrameRange{Lo: lo, Hi: hi} }
	cases := []struct {
		name    string
		overlap []audit.FrameRange
		total   int
		want    []audit.FrameRange
	}{
		{"tail differs", []audit.FrameRange{fr(0, 187)}, 234, []audit.FrameRange{fr(187, 234)}},
		{"full overlap -> empty (not separable)", []audit.FrameRange{fr(0, 234)}, 234, nil},
		{"window straddle past seam", []audit.FrameRange{fr(0, 210)}, 234, []audit.FrameRange{fr(210, 234)}},
		{"no overlap -> whole thing", nil, 234, []audit.FrameRange{fr(0, 234)}},
		{"gap in middle", []audit.FrameRange{fr(10, 50), fr(100, 150)}, 200, []audit.FrameRange{fr(0, 10), fr(50, 100), fr(150, 200)}},
		{"overlap Hi exceeds total is clamped", []audit.FrameRange{fr(0, 250)}, 234, nil},
		{"unsorted overlap", []audit.FrameRange{fr(100, 150), fr(10, 50)}, 200, []audit.FrameRange{fr(0, 10), fr(50, 100), fr(150, 200)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := complementRanges(c.overlap, c.total)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("complementRanges(%v,%d) = %v, want %v", c.overlap, c.total, got, c.want)
			}
		})
	}
}

func TestSumFrames(t *testing.T) {
	got := sumFrames([]audit.FrameRange{{Lo: 0, Hi: 10}, {Lo: 20, Hi: 25}})
	if got != 15 {
		t.Fatalf("sumFrames = %d, want 15", got)
	}
	if sumFrames(nil) != 0 {
		t.Fatalf("sumFrames(nil) != 0")
	}
}

func TestTwinDiscriminative_UpsertGet(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "twindisc-repo"})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(title, sha string) uuid.UUID {
		m, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
			ClientID: cli.ID, Title: title, DurationSeconds: 30,
			MasterStoragePath: "/tmp", MasterSHA256: sha,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	aID, bID := mk("twindisc-A", "twindisc-sha-A"), mk("twindisc-B", "twindisc-sha-B")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM material_twin_discriminative WHERE material_id IN ($1,$2)", aID, bID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id IN ($1,$2)", aID, bID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewTwinDiscriminative(pool)
	disc := []audit.FrameRange{{Lo: 187, Hi: 234}}
	if err := repo.Upsert(ctx, aID, bID, disc, 47); err != nil {
		t.Fatal(err)
	}

	gotDisc, gotFrames, err := repo.Get(ctx, aID, bID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFrames != 47 || !reflect.DeepEqual(gotDisc, disc) {
		t.Fatalf("Get = %v frames=%d, want %v frames=47", gotDisc, gotFrames, disc)
	}

	// Upsert again with new values → ON CONFLICT updates in place.
	disc2 := []audit.FrameRange{{Lo: 100, Hi: 120}, {Lo: 200, Hi: 234}}
	if err := repo.Upsert(ctx, aID, bID, disc2, 54); err != nil {
		t.Fatal(err)
	}
	gotDisc, gotFrames, _ = repo.Get(ctx, aID, bID)
	if gotFrames != 54 || !reflect.DeepEqual(gotDisc, disc2) {
		t.Fatalf("after re-upsert Get = %v frames=%d, want %v frames=54", gotDisc, gotFrames, disc2)
	}

	// Non-existent pair → (nil, 0, nil).
	d, f, err := repo.Get(ctx, aID, uuid.New())
	if err != nil || d != nil || f != 0 {
		t.Fatalf("Get(absent) = %v,%d,%v want nil,0,nil", d, f, err)
	}

	// disc_frames = 0 with empty ranges (not-separable pair) round-trips.
	if err := repo.Upsert(ctx, bID, aID, nil, 0); err != nil {
		t.Fatal(err)
	}
	d, f, err = repo.Get(ctx, bID, aID)
	if err != nil || len(d) != 0 || f != 0 {
		t.Fatalf("Get(empty disc) = %v,%d,%v want nil/empty,0,nil", d, f, err)
	}
}
