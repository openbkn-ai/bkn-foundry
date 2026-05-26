package chelper

import (
	"errors"
	"testing"

	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp/cmpmock"
	"go.uber.org/mock/gomock"
	//"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/test/mock_log"
	//"go.uber.org/mock/gomock"
)

func TestRecordErrLogWithPos(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	logger := cmpmock.NewMockLogger(ctrl)
	logger.EXPECT().Errorln(gomock.Any()).AnyTimes()

	err := errors.New("test error")
	RecordErrLogWithPos(logger, err, "test")
}
