/*
Package sqlxmock is a mock library implementing sql driver. Which has one and only
purpose - to simulate any sql driver behavior in tests, without needing a real
database connection. It helps to maintain correct **TDD** workflow.
It does not require any modifications to your source code in order to test
and mock database operations. Supports concurrency and multiple database mocking.
The driver allows to mock any sql driver method behavior.
*/
package sqlx

import (
	"github.com/DATA-DOG/go-sqlmock"
)

func New() (*DB, sqlmock.Sqlmock, error) {
	db, mock, err := sqlmock.New()

	return &DB{
		reader: db,
		writer: db,
	}, mock, err
}
