package operator

import (
	"database/sql"
	"fmt"
)

func finishTx(tx *sql.Tx, rollback bool) (err error) {
	if tx == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("finish tx panic: %v", r)
		}
	}()
	if rollback {
		return tx.Rollback()
	}
	return tx.Commit()
}
