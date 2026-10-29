package settlement

import (
	"context"
	"testing"
	"time"

	"github.com/filspay/settlement/internal/money"
)

// TestCloseBatch_SchemeAmountMatchesTruncatedFils pins ADR-0002's rule that
// the scheme file amount is exactly amount_fils / 1000. Amounts whose third
// decimal is >= 5 used to be rounded up from the gross (4755 -> "4.76")
// while amount_fils was truncated (4750), so the scheme total always ran
// over ours.
func TestCloseBatch_SchemeAmountMatchesTruncatedFils(t *testing.T) {
	pool := mustPool(t)
	const mdrBps = 250
	merchantID := insertActiveMerchant(t, pool, mdrBps)
	businessDate := time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC)
	start, _ := BusinessDayWindow(businessDate)

	for i, amount := range []money.Fils{4755, 4747, 1005, 12349} {
		insertCaptureEvent(t, pool, merchantID, mdrBps, amount, start.Add(time.Duration(i+1)*time.Hour))
	}

	result, err := CloseBatch(context.Background(), pool, merchantID, businessDate)
	if err != nil {
		t.Fatalf("CloseBatch: %v", err)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT line_sequence, amount_fils, amount::text
		  FROM scheme_export_lines
		 WHERE settlement_batch_id = $1
		 ORDER BY line_sequence
	`, result.BatchID)
	if err != nil {
		t.Fatalf("query scheme_export_lines: %v", err)
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var seq int32
		var amountFils int64
		var amount string
		if err := rows.Scan(&seq, &amountFils, &amount); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		if want := formatFilsAsTwoDecimalString(money.Fils(amountFils)); amount != want {
			t.Errorf("line %d: amount = %s, want %s (amount_fils %d / 1000)", seq, amount, want, amountFils)
		}
	}
	if n != 4 {
		t.Fatalf("expected 4 lines, got %d", n)
	}
}
