package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/kowell-ai/kowell-core/adp/dataflow/flow-automation/common"
	liberrors "github.com/kowell-ai/kowell-core/adp/dataflow/flow-automation/libs/go/errors"
	i18n "github.com/kowell-ai/kowell-core/adp/dataflow/flow-automation/libs/go/i18n"
)

func TestMain(t *testing.T) {

	i18n.InitI18nTranslator(common.MultiResourcePath)

	err := liberrors.NewPublicRestError(context.Background(), liberrors.PErrorInternalServerError,
		liberrors.PErrorInternalServerError,
		nil)
	fmt.Println(err)
}
