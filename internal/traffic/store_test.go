package traffic

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/database"
)

func TestSamplePersistsDeltasAndAppliesQuota(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/db", 0755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	if _, err = db.DB.Exec(`INSERT INTO traffic_counters(node_id,period,quota_bytes,reset_day,updated_at) VALUES(?,?,?,?,?)`, "node-a", BillingPeriod(now, 5), 1000, 5, database.Now()); err != nil {
		t.Fatal(err)
	}
	store := New(db.DB)
	first, err := store.Sample(ctx, "node-a", 100, 200, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeltaUpload != 0 || first.DeltaDownload != 0 {
		t.Fatalf("first sample must only establish a baseline: %+v", first)
	}
	second, err := store.Sample(ctx, "node-a", 400, 900, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.DeltaUpload != 300 || second.DeltaDownload != 700 || second.Firewall != "pause" {
		t.Fatalf("unexpected second sample: %+v", second)
	}
	values, err := store.List(ctx)
	if err != nil || len(values) != 1 {
		t.Fatalf("list failed: %v %+v", err, values)
	}
	if values[0].UploadBytes != 300 || values[0].DownloadBytes != 700 || !values[0].PausedByQuota {
		t.Fatalf("unexpected persisted counter: %+v", values[0])
	}
}

func TestBillingPeriodUsesConfiguredResetDay(t *testing.T) {
	before := time.Date(2026, time.August, 4, 23, 0, 0, 0, time.UTC)
	after := before.Add(2 * time.Hour)
	if got := BillingPeriod(before, 5); got != "2026-07-05" {
		t.Fatalf("unexpected previous cycle: %s", got)
	}
	if got := BillingPeriod(after, 5); got != "2026-08-05" {
		t.Fatalf("unexpected new cycle: %s", got)
	}
}

func TestProjectQuotaUsesIndependentPeriodAndPause(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/db", 0755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db.DB)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	if err = store.SetProjectQuota(ctx, 1000, 5); err != nil {
		t.Fatal(err)
	}
	result, err := store.SampleProject(ctx, 400, 600, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Firewall != "pause" || !result.Paused || result.TotalUpload != 400 || result.TotalDownload != 600 {
		t.Fatalf("unexpected project result: %+v", result)
	}
	if err = store.SetProjectQuota(ctx, 2000, 5); err != nil {
		t.Fatal(err)
	}
	result, err = store.SampleProject(ctx, 0, 0, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.Firewall != "resume" || result.Paused {
		t.Fatalf("raising the project quota should resume it: %+v", result)
	}
	if err = store.ResetProject(ctx); err != nil {
		t.Fatal(err)
	}
	counter, err := store.Project(ctx)
	if err != nil || counter.Paused || counter.UploadBytes != 0 || counter.DownloadBytes != 0 {
		t.Fatalf("project reset failed: %+v, %v", counter, err)
	}
}

func TestAnyConnectUserQuotaIsIndependent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/db", 0755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = db.DB.Exec(`INSERT INTO nodes(id,type,name,enabled,desired_state,runtime_version,listen_host,listen_port,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "node-a", "anyconnect", "AnyConnect", 1, "stopped", "v1.5.0", "127.0.0.1", 443, database.Now(), database.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`INSERT INTO secrets(id,kind,ciphertext,nonce,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "secret-a", "test", []byte("x"), []byte("n"), database.Now(), database.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`INSERT INTO anyconnect_users(id,node_id,username,password_secret_ref,route_group,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "user-a", "node-a", "alice", "secret-a", "full", 1, database.Now(), database.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`INSERT INTO anyconnect_user_traffic(user_id,period,reset_day,updated_at) VALUES(?,?,?,?)`, "user-a", "2026-08-05", 5, database.Now()); err != nil {
		t.Fatal(err)
	}
	store := New(db.DB)
	if err = store.SetUserQuota(ctx, "user-a", 2*1024*1024*1024, 5); err != nil {
		t.Fatal(err)
	}
	counter, err := store.GetUser(ctx, "user-a")
	if err != nil || counter.QuotaBytes != 2*1024*1024*1024 || counter.ResetDay != 5 {
		t.Fatalf("unexpected user quota: %+v, %v", counter, err)
	}
	if err = store.ResetUser(ctx, "user-a"); err != nil {
		t.Fatal(err)
	}
	counter, err = store.GetUser(ctx, "user-a")
	if err != nil || counter.UploadBytes != 0 || counter.DownloadBytes != 0 || counter.QuotaBytes != 2*1024*1024*1024 {
		t.Fatalf("unexpected user reset: %+v, %v", counter, err)
	}
}

func TestSampleUserPersistsDeltaAndAppliesQuota(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/db", 0755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = db.DB.Exec(`INSERT INTO nodes(id,type,name,enabled,desired_state,runtime_version,listen_host,listen_port,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "node-a", "anyconnect", "AnyConnect", 1, "running", "v1.5.0", "127.0.0.1", 443, database.Now(), database.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`INSERT INTO secrets(id,kind,ciphertext,nonce,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "secret-a", "test", []byte("x"), []byte("n"), database.Now(), database.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`INSERT INTO anyconnect_users(id,node_id,username,password_secret_ref,route_group,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "user-a", "node-a", "alice", "secret-a", "full", 1, database.Now(), database.Now()); err != nil {
		t.Fatal(err)
	}
	store := New(db.DB)
	if err = store.SetUserQuota(ctx, "user-a", 1000, 1); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	first, err := store.SampleUser(ctx, "user-a", 100, 200, now)
	if err != nil || first.DeltaUpload != 0 || first.DeltaDownload != 0 {
		t.Fatalf("first user sample should establish a baseline: %+v, %v", first, err)
	}
	second, err := store.SampleUser(ctx, "user-a", 400, 900, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.DeltaUpload != 300 || second.DeltaDownload != 700 || second.Firewall != "pause" || !second.Paused {
		t.Fatalf("unexpected user sample: %+v", second)
	}
	counter, err := store.GetUser(ctx, "user-a")
	if err != nil || counter.UploadBytes != 300 || counter.DownloadBytes != 700 || !counter.PausedByQuota {
		t.Fatalf("unexpected persisted user counter: %+v, %v", counter, err)
	}
}
