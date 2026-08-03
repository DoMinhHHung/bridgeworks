package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		action          = flag.String("action", "", "generate or sign")
		privateKeyPath  = flag.String("private-key", "", "private key input/output path")
		publicKeyPath   = flag.String("public-key", "", "public verification key body output path")
		outputPath      = flag.String("output", "", "signed token output path; stdout when empty")
		issuer          = flag.String("issuer", "", "iss claim")
		subject         = flag.String("subject", "", "sub claim")
		sessionID       = flag.String("session-id", "", "sid claim")
		authorizedParty = flag.String("authorized-party", "", "azp claim")
		ttl             = flag.Duration("ttl", 5*time.Minute, "token validity from now")
		notBeforeOffset = flag.Duration("not-before-offset", -time.Minute, "nbf offset from now")
	)
	flag.Parse()

	var err error
	switch *action {
	case "generate":
		err = generateKeyPair(*privateKeyPath, *publicKeyPath)
	case "sign":
		err = signSessionToken(signParams{
			privateKeyPath:  *privateKeyPath,
			outputPath:      *outputPath,
			issuer:          *issuer,
			subject:         *subject,
			sessionID:       *sessionID,
			authorizedParty: *authorizedParty,
			ttl:             *ttl,
			notBeforeOffset: *notBeforeOffset,
		})
	default:
		err = errors.New("action must be generate or sign")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Clerk session token helper failed")
		os.Exit(1)
	}
}

func generateKeyPair(privateKeyPath, publicKeyPath string) error {
	if strings.TrimSpace(privateKeyPath) == "" || strings.TrimSpace(publicKeyPath) == "" {
		return errors.New("private-key and public-key paths are required")
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if err := os.WriteFile(privateKeyPath, privatePEM, 0o600); err != nil {
		return err
	}

	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}
	publicKeyBody := base64.StdEncoding.EncodeToString(publicDER)
	return os.WriteFile(publicKeyPath, []byte(publicKeyBody), 0o600)
}

type signParams struct {
	privateKeyPath  string
	outputPath      string
	issuer          string
	subject         string
	sessionID       string
	authorizedParty string
	ttl             time.Duration
	notBeforeOffset time.Duration
}

func signSessionToken(params signParams) error {
	if strings.TrimSpace(params.privateKeyPath) == "" ||
		strings.TrimSpace(params.issuer) == "" ||
		strings.TrimSpace(params.subject) == "" ||
		strings.TrimSpace(params.sessionID) == "" ||
		strings.TrimSpace(params.authorizedParty) == "" {
		return errors.New("private key and Clerk claims are required")
	}
	if params.ttl <= 0 {
		return errors.New("ttl must be greater than zero")
	}

	privatePEM, err := os.ReadFile(params.privateKeyPath)
	if err != nil {
		return err
	}
	privateKey, err := parsePrivateKey(privatePEM)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	headerJSON, err := json.Marshal(map[string]any{
		"alg": "RS256",
		"typ": "JWT",
	})
	if err != nil {
		return err
	}
	claimsJSON, err := json.Marshal(map[string]any{
		"iss": strings.TrimSpace(params.issuer),
		"sub": strings.TrimSpace(params.subject),
		"sid": strings.TrimSpace(params.sessionID),
		"azp": strings.TrimSpace(params.authorizedParty),
		"iat": now.Add(-time.Minute).Unix(),
		"nbf": now.Add(params.notBeforeOffset).Unix(),
		"exp": now.Add(params.ttl).Unix(),
	})
	if err != nil {
		return err
	}

	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return err
	}
	token := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)

	if strings.TrimSpace(params.outputPath) == "" {
		_, err = fmt.Fprintln(os.Stdout, token)
		return err
	}
	return os.WriteFile(params.outputPath, []byte(token), 0o600)
}

func parsePrivateKey(value []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, errors.New("invalid private key")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	privateKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return privateKey, nil
}
