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
		action                  = flag.String("action", "", "generate or sign")
		privateKeyPath          = flag.String("private-key", "", "private key input/output path")
		publicKeyPath           = flag.String("public-key", "", "public verification key body output path")
		outputPath              = flag.String("output", "", "signed token output path; stdout when empty")
		issuer                  = flag.String("issuer", "", "iss claim")
		subject                 = flag.String("subject", "", "sub claim")
		sessionID               = flag.String("session-id", "", "sid claim")
		authorizedParty         = flag.String("authorized-party", "", "azp claim")
		organizationID          = flag.String("organization-id", "", "optional verified active organization ID")
		organizationRole        = flag.String("organization-role", "", "optional organization role test claim")
		organizationPermissions = flag.String("organization-permissions", "", "optional comma-separated organization permissions test claim")
		ttl                     = flag.Duration("ttl", 5*time.Minute, "token validity from now")
		notBeforeOffset         = flag.Duration("not-before-offset", -time.Minute, "nbf offset from now")
	)
	flag.Parse()
	var err error
	switch *action {
	case "generate":
		err = generateKeyPair(*privateKeyPath, *publicKeyPath)
	case "sign":
		err = signSessionToken(signParams{
			privateKeyPath: *privateKeyPath, outputPath: *outputPath, issuer: *issuer, subject: *subject,
			sessionID: *sessionID, authorizedParty: *authorizedParty,
			organizationID: *organizationID, organizationRole: *organizationRole,
			organizationPermissions: splitNonEmpty(*organizationPermissions), ttl: *ttl, notBeforeOffset: *notBeforeOffset,
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
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}
	return os.WriteFile(publicKeyPath, []byte(base64.StdEncoding.EncodeToString(publicDER)), 0o600)
}

type signParams struct {
	privateKeyPath, outputPath, issuer, subject, sessionID, authorizedParty string
	organizationID, organizationRole                                        string
	organizationPermissions                                                 []string
	ttl, notBeforeOffset                                                    time.Duration
}

func signSessionToken(params signParams) error {
	if strings.TrimSpace(params.privateKeyPath) == "" || strings.TrimSpace(params.issuer) == "" || strings.TrimSpace(params.subject) == "" || strings.TrimSpace(params.sessionID) == "" || strings.TrimSpace(params.authorizedParty) == "" {
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
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return err
	}
	claims := map[string]any{
		"iss": strings.TrimSpace(params.issuer), "sub": strings.TrimSpace(params.subject),
		"sid": strings.TrimSpace(params.sessionID), "azp": strings.TrimSpace(params.authorizedParty),
		"iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(params.notBeforeOffset).Unix(), "exp": now.Add(params.ttl).Unix(),
	}
	if value := strings.TrimSpace(params.organizationID); value != "" {
		claims["org_id"] = value
	}
	if value := strings.TrimSpace(params.organizationRole); value != "" {
		claims["org_role"] = value
	}
	if len(params.organizationPermissions) > 0 {
		claims["org_permissions"] = params.organizationPermissions
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
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
func splitNonEmpty(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
