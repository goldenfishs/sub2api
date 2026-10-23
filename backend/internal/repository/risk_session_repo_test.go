package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRiskSessionPayloadStorageFailurePreservesCommittedBlock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO risk_session_incidents").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(101))
	mock.ExpectExec("INSERT INTO risk_session_keys").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("UPDATE risk_session_incidents SET encrypted_snapshot").WillReturnError(errors.New("disk full"))
	item := &service.RiskSessionIncident{UserID: 7, Source: "local_audit", SnapshotStatus: "stored", EncryptedSnapshot: "cipher", CreatedAt: time.Now()}
	require.NoError(t, NewRiskSessionRepository(db).Create(context.Background(), item, []string{"hash"}))
	require.EqualValues(t, 101, item.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRiskSessionFingerprintFailureRollsBackIncident(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO risk_session_incidents").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(101))
	mock.ExpectExec("INSERT INTO risk_session_keys").WillReturnError(errors.New("unavailable"))
	mock.ExpectRollback()
	err = NewRiskSessionRepository(db).Create(context.Background(), &service.RiskSessionIncident{UserID: 7}, []string{"hash"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
