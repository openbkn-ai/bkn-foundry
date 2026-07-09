package logics

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	mock_interfaces "vega-backend/interfaces/mock"
)

func TestDrivenAccessSetters(t *testing.T) {
	ctrl := gomock.NewController(t)

	db := &sql.DB{}
	authAccess := mock_interfaces.NewMockAuthAccess(ctrl)
	asynqAccess := mock_interfaces.NewMockAsynqAccess(ctrl)
	buildTaskAccess := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	catalogAccess := mock_interfaces.NewMockCatalogAccess(ctrl)
	connectorTypeAccess := mock_interfaces.NewMockConnectorTypeAccess(ctrl)
	discoverScheduleAccess := mock_interfaces.NewMockDiscoverScheduleAccess(ctrl)
	discoverTaskAccess := mock_interfaces.NewMockDiscoverTaskAccess(ctrl)
	kafkaAccess := mock_interfaces.NewMockKafkaAccess(ctrl)
	modelFactoryAccess := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	permissionAccess := mock_interfaces.NewMockPermissionAccess(ctrl)
	resourceAccess := mock_interfaces.NewMockResourceAccess(ctrl)
	userMgmtAccess := mock_interfaces.NewMockUserMgmtAccess(ctrl)

	SetDB(db)
	SetAuthAccess(authAccess)
	SetAsynqAccess(asynqAccess)
	SetBuildTaskAccess(buildTaskAccess)
	SetCatalogAccess(catalogAccess)
	SetConnectorTypeAccess(connectorTypeAccess)
	SetDiscoverScheduleAccess(discoverScheduleAccess)
	SetDiscoverTaskAccess(discoverTaskAccess)
	SetKafkaAccess(kafkaAccess)
	SetModelFactoryAccess(modelFactoryAccess)
	SetPermissionAccess(permissionAccess)
	SetResourceAccess(resourceAccess)
	SetUserMgmtAccess(userMgmtAccess)

	assert.Same(t, db, DB)
	assert.Same(t, authAccess, AA)
	assert.Same(t, asynqAccess, AQA)
	assert.Same(t, buildTaskAccess, BTA)
	assert.Same(t, catalogAccess, CA)
	assert.Same(t, connectorTypeAccess, CTA)
	assert.Same(t, discoverScheduleAccess, DSA)
	assert.Same(t, discoverTaskAccess, DTA)
	assert.Same(t, kafkaAccess, KA)
	assert.Same(t, modelFactoryAccess, MFA)
	assert.Same(t, permissionAccess, PA)
	assert.Same(t, resourceAccess, RA)
	assert.Same(t, userMgmtAccess, UMA)
}
