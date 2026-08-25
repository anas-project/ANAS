package runner

import (
	"errors"

	"github.com/anas-project/ANAS/internal/application"
)

// applicationCLIError keeps the established CLI exit-code boundary while the
// typed application service remains transport-neutral. HTTP maps the same
// error kind to a status code; the CLI maps it to its documented process code.
func applicationCLIError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *application.Error
	if !errors.As(err, &appErr) {
		return failuref("internal", "%s", err.Error())
	}
	switch appErr.Kind {
	case application.ErrorKindInvalidArgument:
		return cliErrorf(exitUsage, appErr.Code, "%s", appErr.Message)
	case application.ErrorKindNotFound, application.ErrorKindFailedPrecondition:
		if appErr.Code == "module_command_confirmation_required" {
			return confirmationErrorf("%s", appErr.Message)
		}
		return preconditionErrorf(appErr.Code, "%s", appErr.Message)
	case application.ErrorKindInternal:
		// An unreadable persisted state is an unmet machine precondition in the
		// existing CLI contract, even though an HTTP server reports the same
		// corrupt state as its own inability to serve the resource.
		if appErr.Code == "state_unreadable" {
			return preconditionErrorf(appErr.Code, "%s", appErr.Message)
		}
		return failuref(appErr.Code, "%s", appErr.Message)
	default:
		return failuref("internal", "%s", appErr.Message)
	}
}
