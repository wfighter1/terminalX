package relay

import "net/http"

// apiError carries an HTTP status for API responses.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errBadRequest(msg string) error { return &apiError{http.StatusBadRequest, msg} }
func errNotFound(msg string) error   { return &apiError{http.StatusNotFound, msg} }
func errConflict(msg string) error   { return &apiError{http.StatusConflict, msg} }
func errOffline(msg string) error    { return &apiError{http.StatusServiceUnavailable, msg} }
