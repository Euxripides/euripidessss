package cryptodownload

import "fmt"

type GUIJobPersistError struct {
	JobID string
	Event string
	Err   error
}

func (e *GUIJobPersistError) Error() string {
	return fmt.Sprintf("persist GUI job %s before %s: %v", e.JobID, e.Event, e.Err)
}

func (e *GUIJobPersistError) Unwrap() error { return e.Err }
