// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	BknBackend_KnEvent_InvalidParameter  = "BknBackend.KnEvent.InvalidParameter"
	BknBackend_KnEvent_EventTypeNotFound = "BknBackend.KnEvent.EventTypeNotFound"
	BknBackend_KnEvent_EventNotFound     = "BknBackend.KnEvent.EventNotFound"
	BknBackend_KnEvent_EventTypeExisted  = "BknBackend.KnEvent.EventTypeExisted"
	BknBackend_KnEvent_EventTypeInUse    = "BknBackend.KnEvent.EventTypeInUse"
	BknBackend_KnEvent_SourceMismatch    = "BknBackend.KnEvent.SourceMismatch"
	BknBackend_KnEvent_ActionNotAllowed  = "BknBackend.KnEvent.ActionNotAllowed"
	BknBackend_KnEvent_TooLarge          = "BknBackend.KnEvent.TooLarge"
	BknBackend_KnEvent_InternalError     = "BknBackend.KnEvent.InternalError"
)

var KnEventErrCodeList = []string{
	BknBackend_KnEvent_InvalidParameter,
	BknBackend_KnEvent_EventTypeNotFound,
	BknBackend_KnEvent_EventNotFound,
	BknBackend_KnEvent_EventTypeExisted,
	BknBackend_KnEvent_EventTypeInUse,
	BknBackend_KnEvent_SourceMismatch,
	BknBackend_KnEvent_ActionNotAllowed,
	BknBackend_KnEvent_TooLarge,
	BknBackend_KnEvent_InternalError,
}
