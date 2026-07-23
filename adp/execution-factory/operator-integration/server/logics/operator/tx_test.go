package operator

import (
	"database/sql"
	"errors"
	"testing"
)

func TestFinishTxRecoversCommitPanic(t *testing.T) {
	err := finishTx(&sql.Tx{}, false)
	if err == nil {
		t.Fatal("expected commit panic to be returned as error")
	}
}

func TestFinishTxRecoversRollbackPanic(t *testing.T) {
	err := finishTx(&sql.Tx{}, true)
	if err == nil {
		t.Fatal("expected rollback panic to be returned as error")
	}
}

func TestFinishTxDoesNotOverrideExistingBusinessError(t *testing.T) {
	businessErr := errors.New("business failed")
	err := businessErr

	finishErr := finishTx(&sql.Tx{}, err != nil)
	if finishErr != nil && err == nil {
		err = finishErr
	}

	if !errors.Is(err, businessErr) {
		t.Fatalf("expected business error to be preserved, got %v", err)
	}
}
