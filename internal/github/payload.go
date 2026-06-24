package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type minimalRepositoryPayload struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	After string `json:"after"`
}

func ExtractRepositoryFullName(body []byte) (string, error) {
	var payload minimalRepositoryPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.Repository.FullName == "" {
		return "", errors.New("payload missing repository.full_name")
	}
	return payload.Repository.FullName, nil
}

func ExtractAfterRevision(body []byte) string {
	var payload minimalRepositoryPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.After
}

func VerifySignature256(body []byte, header string, secret []byte) bool {
	const prefix = "sha256="
	if len(secret) == 0 || !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
