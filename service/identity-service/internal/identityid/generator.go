package identityid

import (
	"crypto/rand"
	"fmt"
	"io"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
)

const (
	idUserPrefix    = "bw"
	digitAcceptCeil = byte(250)
)

type Clock func() time.Time

type Generator struct {
	clock    Clock
	random   io.Reader
	location *time.Location
}

type UUIDV7Generator struct{}

func (UUIDV7Generator) New() (uuid.UUID, error) {
	return uuid.NewV7()
}

func New(clock Clock, random io.Reader) (*Generator, error) {
	location, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		return nil, fmt.Errorf("load Asia/Ho_Chi_Minh timezone: %w", err)
	}
	if clock == nil {
		clock = time.Now
	}
	if random == nil {
		random = rand.Reader
	}

	return &Generator{
		clock:    clock,
		random:   random,
		location: location,
	}, nil
}

func (g *Generator) Generate() (string, error) {
	if g == nil || g.clock == nil || g.random == nil || g.location == nil {
		return "", fmt.Errorf("id_user generator is not initialized")
	}

	var digits [6]byte
	for index := range digits {
		digit, err := secureDigit(g.random)
		if err != nil {
			return "", fmt.Errorf("generate secure id_user digit: %w", err)
		}
		digits[index] = '0' + digit
	}

	date := g.clock().In(g.location).Format("020106")
	return idUserPrefix + string(digits[:4]) + date + string(digits[4:]), nil
}

func secureDigit(source io.Reader) (byte, error) {
	var value [1]byte
	for {
		if _, err := io.ReadFull(source, value[:]); err != nil {
			return 0, err
		}
		if value[0] < digitAcceptCeil {
			return value[0] % 10, nil
		}
	}
}
