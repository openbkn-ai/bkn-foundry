// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var actionScheduleEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.ActionSchedule.InvalidParameter":                  "Invalid parameter. Please check if the parameters are correct.",
		"BknBackend.ActionSchedule.InvalidCronExpression":             "Invalid cron expression. Please check if the cron expression format is correct.",
		"BknBackend.ActionSchedule.InvalidStatus":                     "Invalid status value. Please check if the status value is correct.",
		"BknBackend.ActionSchedule.ActionTypeNotFound":                "Action type not found. Please check if the action type ID is correct.",
		"BknBackend.ActionSchedule.NotFound":                          "Action schedule not found. Please check if the action schedule ID is correct.",
		"BknBackend.ActionSchedule.InternalError":                     "Internal error. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"BknBackend.ActionSchedule.CreateFailed":                      "Failed to create action schedule. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"BknBackend.ActionSchedule.UpdateFailed":                      "Failed to update action schedule. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"BknBackend.ActionSchedule.DeleteFailed":                      "Failed to delete action schedule. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"BknBackend.ActionSchedule.GetFailed":                         "Failed to get action schedule. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"BknBackend.ActionSchedule.GetActionTypeFailed":               "Failed to get action type. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"BknBackend.ActionSchedule.Detail.NameRequired":               "name is required.",
		"BknBackend.ActionSchedule.Detail.NameLength":                 "name must not exceed 100 characters.",
		"BknBackend.ActionSchedule.Detail.ActionTypeIDRequired":       "action_type_id is required.",
		"BknBackend.ActionSchedule.Detail.CronExpressionRequired":     "cron_expression is required.",
		"BknBackend.ActionSchedule.Detail.InstanceIdentitiesRequired": "_instance_identities is required and must not be empty.",
		"BknBackend.ActionSchedule.Detail.StatusInvalid":              "status must be active or inactive.",
		"BknBackend.ActionSchedule.Detail.UpdateFieldRequired":        "At least one field must be provided for update.",
		"BknBackend.ActionSchedule.Detail.CronExpressionInvalid":      "The cron expression is invalid.",
		"BknBackend.ActionSchedule.Detail.ActionTypeNotFound":         "Action type {{.actionTypeID}} does not exist.",
		"BknBackend.ActionSchedule.Detail.StatusValueInvalid":         "status must be active or inactive; received {{.status}}.",
		"BknBackend.ActionSchedule.Detail.ScheduleNotFound":           "Action schedule {{.scheduleID}} does not exist.",
		"BknBackend.ActionSchedule.Detail.ScheduleScopeMismatch":      "Action schedule {{.scheduleID}} does not belong to knowledge network {{.knID}} on branch {{.branch}}.",
	},
}
