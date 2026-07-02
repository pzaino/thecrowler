package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	imap "github.com/emersion/go-imap"
)

var errIMAPMessageNotFound = errors.New("mail: IMAP message not found")

type imapDiagnosticCause struct {
	detail string
}

func (e *imapDiagnosticCause) Error() string {
	return "imap diagnostic: " + e.detail
}

type imapFailure struct {
	kind       ErrorKind
	message    string
	diagnostic string
}

func translateIMAPError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}

	var mailErr *Error
	if errors.As(err, &mailErr) {
		return err
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return newIMAPError(operation, imapFailure{
			kind: ErrorTimeout, message: "IMAP operation timed out", diagnostic: "deadline exceeded",
		}, context.DeadlineExceeded)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return newIMAPError(operation, imapFailure{
				kind: ErrorTimeout, message: "IMAP operation timed out", diagnostic: "network timeout",
			}, nil)
		}
		return newIMAPError(operation, imapFailure{
			kind: ErrorNetwork, message: "IMAP network operation failed", diagnostic: "network failure",
		}, nil)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return newIMAPError(operation, imapFailure{
			kind: ErrorNetwork, message: "IMAP network operation failed", diagnostic: "connection closed",
		}, nil)
	}
	if errors.Is(err, errIMAPMessageNotFound) {
		return newIMAPError(operation, imapFailure{
			kind: ErrorMessageNotFound, message: "IMAP message was not found", diagnostic: "message not found",
		}, nil)
	}

	responseType, responseCode, responseInfo := imapResponseDetails(err)
	failure := classifyIMAPFailure(operation, responseType, responseCode, responseInfo)
	return newIMAPError(operation, failure, nil)
}

func newIMAPError(operation string, failure imapFailure, safeCause error) error {
	if safeCause == nil && failure.diagnostic != "" {
		safeCause = &imapDiagnosticCause{detail: failure.diagnostic}
	}
	return &Error{
		Kind: failure.kind, Operation: operation, Message: failure.message, Cause: safeCause,
	}
}

func imapResponseDetails(err error) (imap.StatusRespType, imap.StatusRespCode, string) {
	var statusErr *imap.ErrStatusResp
	if errors.As(err, &statusErr) && statusErr.Resp != nil {
		return statusErr.Resp.Type, statusErr.Resp.Code, statusErr.Resp.Info
	}
	return "", "", err.Error()
}

func classifyIMAPFailure(operation string, responseType imap.StatusRespType, responseCode imap.StatusRespCode, responseInfo string) imapFailure {
	code := strings.ToUpper(strings.TrimSpace(string(responseCode)))
	info := strings.ToUpper(strings.TrimSpace(responseInfo))

	if operation == "authenticate" || matchesIMAPToken(code, info,
		"AUTHENTICATIONFAILED", "AUTHORIZATIONFAILED", "INVALID CREDENTIAL", "AUTHENTICATION FAILED", "LOGIN FAILED") {
		return imapFailure{ErrorAuthentication, "IMAP authentication failed", diagnosticForIMAPResponse("authentication failure", code)}
	}
	if matchesIMAPToken(code, info, "LIMIT", "RATELIMIT", "RATE LIMIT", "THROTTL", "TOO MANY REQUESTS", "OVERQUOTA") {
		return imapFailure{ErrorRateLimit, "IMAP request was rate limited", diagnosticForIMAPResponse("rate limited", code)}
	}
	if operationUsesMailbox(operation) && matchesIMAPToken(code, info,
		"NONEXISTENT", "MAILBOX NOT FOUND", "NO SUCH MAILBOX", "MAILBOX DOES NOT EXIST", "UNKNOWN MAILBOX") {
		return imapFailure{ErrorMailboxNotFound, "IMAP mailbox was not found", diagnosticForIMAPResponse("mailbox not found", code)}
	}
	if operationUsesMessage(operation) && matchesIMAPToken(code, info,
		"NONEXISTENT", "MESSAGE NOT FOUND", "NO SUCH MESSAGE", "MESSAGE DOES NOT EXIST", "UNKNOWN MESSAGE") {
		return imapFailure{ErrorMessageNotFound, "IMAP message was not found", diagnosticForIMAPResponse("message not found", code)}
	}
	if matchesIMAPToken(code, info, "NOPERM", "PERMISSION DENIED", "NOT AUTHORIZED") {
		return imapFailure{ErrorPermission, "IMAP operation was not permitted", diagnosticForIMAPResponse("permission denied", code)}
	}
	if responseType == imap.StatusRespBad || matchesIMAPToken(code, info,
		"CANNOT", "CLIENTBUG", "BADCHARSET", "UNKNOWN-CTE", "UNSUPPORTED", "NOT SUPPORTED", "UNKNOWN COMMAND") {
		return imapFailure{ErrorUnsupported, "IMAP operation is not supported", diagnosticForIMAPResponse("unsupported operation", code)}
	}
	return imapFailure{ErrorTransient, "IMAP operation failed", diagnosticForIMAPResponse("temporary protocol failure", code)}
}

func matchesIMAPToken(code, info string, tokens ...string) bool {
	for _, token := range tokens {
		if code == token || strings.Contains(info, token) {
			return true
		}
	}
	return false
}

func operationUsesMailbox(operation string) bool {
	return strings.Contains(operation, "mailbox")
}

func operationUsesMessage(operation string) bool {
	return strings.Contains(operation, "message")
}

func diagnosticForIMAPResponse(fallback, code string) string {
	if code == "" {
		return fallback
	}
	return fmt.Sprintf("%s (%s)", fallback, safeIMAPResponseCode(code))
}

func safeIMAPResponseCode(code string) string {
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return "response code omitted"
		}
	}
	if len(code) > 64 {
		return "response code omitted"
	}
	return code
}
