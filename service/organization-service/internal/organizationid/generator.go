package organizationid

import "github.com/google/uuid"

type Generator interface {
	New() (uuid.UUID, error)
}

type UUIDV7Generator struct{}

func (UUIDV7Generator) New() (uuid.UUID, error) {
	return uuid.NewV7()
}
