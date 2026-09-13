package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestOperationCheckAllSeparatesDenialFromDependencyFailure guards #1533.
//
// bkn-safe answers a denial as 200 {"allowed": false}. Every other outcome (a
// transport error, a timeout, a non-2xx status, a malformed body) means no
// decision was made, so it must not be reported as a confirmed denial.
func TestOperationCheckAllSeparatesDenialFromDependencyFailure(t *testing.T) {
	accessor := &interfaces.AuthAccessor{ID: "user-1", Type: interfaces.AccessorTypeUser}

	Convey("OperationCheckAll", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		authorization := mocks.NewMockAuthorization(ctrl)
		logger := mocks.NewMockLogger(ctrl)
		logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
		service := &authServiceImpl{logger: logger, authorization: authorization}

		Convey("returns the decision when bkn-safe allows", func() {
			authorization.EXPECT().OperationCheck(gomock.Any(), gomock.Any()).
				Return(&interfaces.AuthOperationCheckResponse{Result: true}, nil)

			allowed, err := service.OperationCheckAll(context.Background(), accessor, interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeExecute)

			So(err, ShouldBeNil)
			So(allowed, ShouldBeTrue)
		})

		Convey("returns a denial without an error when bkn-safe denies", func() {
			authorization.EXPECT().OperationCheck(gomock.Any(), gomock.Any()).
				Return(&interfaces.AuthOperationCheckResponse{Result: false}, nil)

			allowed, err := service.OperationCheckAll(context.Background(), accessor, interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeExecute)

			So(err, ShouldBeNil)
			So(allowed, ShouldBeFalse)
		})

		Convey("fails closed as 503 when bkn-safe is unreachable, and keeps the cause server-side", func() {
			cause := errors.New(`bkn-safe POST /api/safe/v1/authz/check: dial tcp 10.43.0.17:8080: i/o timeout`)
			authorization.EXPECT().OperationCheck(gomock.Any(), gomock.Any()).Return(nil, cause)
			var logged string
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(format string, args ...interface{}) { logged = fmt.Sprintf(format, args...) })

			allowed, err := service.OperationCheckAll(context.Background(), accessor, interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeExecute)

			So(allowed, ShouldBeFalse)
			var httpErr *oerrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusServiceUnavailable)
			So(strings.HasSuffix(httpErr.Code, "."+oerrors.ErrExtCommonAuthorizationUnavailable.String()), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldBeNil)
			So(err.Error(), ShouldNotContainSubstring, "10.43.0.17")
			So(err.Error(), ShouldNotContainSubstring, "/api/safe/v1/authz/check")
			So(logged, ShouldContainSubstring, cause.Error())
		})
	})
}
