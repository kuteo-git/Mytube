package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// Against the real cast schema, inside a transaction that is rolled back, so
// nothing is left behind. Skipped unless CAST_TEST_DATABASE_URL is set:
//
//	CAST_TEST_DATABASE_URL='postgres://cast_svc:cast_dev@localhost:5432/localyoutube?search_path=cast' \
//	  go test ./services/cast/internal/adapter/postgres/
func inTx(t *testing.T) (*Repo, context.Context) {
	t.Helper()
	url := os.Getenv("CAST_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CAST_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(ctx)
		_ = conn.Close(ctx)
	})
	return New(tx), ctx
}

var now = time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC)

func session(device string) domain.Session {
	return domain.Session{
		ID: uuid.NewString(), DeviceID: device, DeviceName: "Loa R1", DeviceKind: domain.KindR1, StartedBy: "u1",
		Queue: []domain.Item{
			{VideoID: "a", Title: "Bài A", Channel: "Kênh", ThumbnailURL: "/media/a/thumb.jpg", DurationS: 245},
			{VideoID: "b", Title: "B", DurationS: 100},
		},
		State: domain.Loading, LoadedMediaKey: "a", LoadingSince: now, LastSeenOK: now,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func TestARoundTripKeepsEveryField(t *testing.T) {
	repo, ctx := inTx(t)
	s := session("test-device-" + uuid.NewString())
	if err := repo.Create(ctx, s, now); err != nil {
		t.Fatal(err)
	}
	s.State, s.PositionS, s.DurationS, s.Volume, s.LoadAcked = domain.Playing, 33, 245, 40, true
	s.PrevMediaKey, s.LastProgressAt, s.Version = "z", now.Add(time.Second), 2
	if err := repo.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceName != "Loa R1" || got.State != domain.Playing || got.PositionS != 33 || got.Volume != 40 || !got.LoadAcked ||
		got.PrevMediaKey != "z" || got.Version != 2 || !got.LastProgressAt.Equal(s.LastProgressAt) {
		t.Fatalf("got %+v", got)
	}
	if len(got.Queue) != 2 || got.Queue[0] != s.Queue[0] {
		t.Fatalf("queue = %+v", got.Queue)
	}
}

func TestANewSessionReplacesTheOneOnTheSameSpeaker(t *testing.T) {
	repo, ctx := inTx(t)
	device := "test-device-" + uuid.NewString()
	first, second := session(device), session(device)
	if err := repo.Create(ctx, first, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, second, now); err != nil {
		t.Fatal(err)
	}
	old, _ := repo.Get(ctx, first.ID)
	if old.State != domain.Ended || old.EndedReason != domain.Replaced {
		t.Fatalf("old state=%s reason=%s", old.State, old.EndedReason)
	}
	if err := repo.Save(ctx, first); !errors.Is(err, domain.ErrEnded) {
		t.Fatalf("saving over a replaced session: err = %v, want ErrEnded", err)
	}
}

func TestTheIndexRefusesTwoActiveSessionsOnOneSpeaker(t *testing.T) {
	repo, ctx := inTx(t)
	device := "test-device-" + uuid.NewString()
	if err := repo.Create(ctx, session(device), now); err != nil {
		t.Fatal(err)
	}
	args, _ := values(session(device))
	_, err := repo.db.Exec(ctx, `INSERT INTO sessions (`+columns+`) VALUES
		($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, args...)
	if err == nil {
		t.Fatal("a second active session on one speaker was accepted")
	}
}

func TestAnUnknownSessionIsNotFound(t *testing.T) {
	repo, ctx := inTx(t)
	if _, err := repo.Get(ctx, uuid.NewString()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
