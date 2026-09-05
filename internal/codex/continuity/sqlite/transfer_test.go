package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"gorm.io/gorm"
)

func TestContinuityPortableOwnersMergeWithoutRewinding(t *testing.T) {
	ctx := context.Background()
	source, closeSource := openTestDB(t)
	defer closeSource()
	target, closeTarget := openTestDB(t)
	defer closeTarget()
	for _, db := range []*gorm.DB{source, target} {
		if err := Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	binding := testBinding(t, accountScope(t, "account", "codex"), codexcontinuity.LifecycleCommitted)
	row, err := encodeBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := Export(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var transferred []TransferBinding
	if err := json.Unmarshal(encoded, &transferred); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := target.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, transferred) }); err != nil {
			t.Fatal(err)
		}
	}
	transferred[0].UpdatedAtNS += int64(time.Hour)
	transferred[0].ExpiresAtNS += int64(time.Hour)
	if err := target.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, transferred) }); err != nil {
		t.Fatal(err)
	}
	if err := target.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, rows) }); err != nil {
		t.Fatal(err)
	}
	actual, err := Export(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if actual[0].UpdatedAtNS != transferred[0].UpdatedAtNS {
		t.Fatal("older backup rewound live owner")
	}
	conflicting := cloneRow(transferred[0])
	conflicting.ClientDigest[0] ^= 1
	if err := target.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, []TransferBinding{conflicting}) }); err == nil {
		t.Fatal("conflicting ownership imported")
	}
	invalid := cloneRow(transferred[0])
	invalid.OpaqueDigest = nil
	if err := Import(ctx, target, []TransferBinding{invalid}); err == nil {
		t.Fatal("invalid digest accepted")
	}
}
