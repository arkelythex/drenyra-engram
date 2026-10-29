package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	ScopeBindingRequired    = "SCOPE_BINDING_REQUIRED"
	ScopeBindingInvalid     = "SCOPE_BINDING_INVALID"
	ScopeMismatch           = "SCOPE_MISMATCH"
	UnsupportedScopeVersion = "UNSUPPORTED_SCOPE_VERSION"
)

type FiscalScopeError struct{ Code, Message string }

func (e *FiscalScopeError) Error() string { return e.Code + ": " + e.Message }

type AuthorityLevel string

const (
	AuthorityLevelAsk     AuthorityLevel = "ASK"
	AuthorityLevelAnalyze AuthorityLevel = "ANALYZE"
	AuthorityLevelPrepare AuthorityLevel = "PREPARE"
	AuthorityLevelExecute AuthorityLevel = "EXECUTE"
)

type FiscalScopeBinding struct {
	Version        string         `json:"version"`
	Tenant         string         `json:"tenant"`
	Organization   string         `json:"organization"`
	Company        string         `json:"company"`
	FiscalPeriod   string         `json:"fiscalPeriod"`
	LedgerBook     string         `json:"ledgerBook"`
	OperationType  string         `json:"operationType"`
	SourceSnapshot string         `json:"sourceSnapshot"`
	PolicyVersion  string         `json:"policyVersion"`
	Actor          string         `json:"actor"`
	AuthorityLevel AuthorityLevel `json:"authorityLevel"`
}

var fiscalScopeKeys = [...]string{"version", "tenant", "organization", "company", "fiscalPeriod", "ledgerBook", "operationType", "sourceSnapshot", "policyVersion", "actor", "authorityLevel"}
var fiscalOperations = map[string]AuthorityLevel{
	"memory.save": AuthorityLevelPrepare, "memory.supersede": AuthorityLevelPrepare,
	"evidence.store": AuthorityLevelPrepare, "evidence.link": AuthorityLevelPrepare,
	"rule.link": AuthorityLevelPrepare, "close.create": AuthorityLevelPrepare,
	"context.read": AuthorityLevelAsk, "review.queue": AuthorityLevelAsk,
	"review.detail": AuthorityLevelAsk, "timeline.read": AuthorityLevelAsk, "evidence.get": AuthorityLevelAsk,
	"reconstruct.read": AuthorityLevelAnalyze, "verify.read": AuthorityLevelAnalyze,
	"memory.approve": AuthorityLevelExecute, "close.approve": AuthorityLevelExecute,
	"period.reopen": AuthorityLevelExecute, "period.write-check": AuthorityLevelExecute,
}

func DecodeFiscalScopeV1JSON(raw []byte) (FiscalScopeBinding, error) {
	if !utf8.Valid(raw) {
		return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "binding must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "binding must be one JSON object")
	}
	values := make([]string, len(fiscalScopeKeys))
	for i, expected := range fiscalScopeKeys {
		if !decoder.More() {
			if i == 1 && values[0] != "v1" {
				return FiscalScopeBinding{}, scopeError(UnsupportedScopeVersion, "unsupported fiscal scope version")
			}
			return FiscalScopeBinding{}, scopeError(ScopeBindingRequired, "complete v1 binding is required")
		}
		key, err := decoder.Token()
		if err != nil || key != expected {
			return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "keys must be unique and in canonical order")
		}
		if err := decoder.Decode(&values[i]); err != nil {
			return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "all binding values must be strings")
		}
		if i == 0 && values[0] != "v1" {
			return FiscalScopeBinding{}, scopeError(UnsupportedScopeVersion, "unsupported fiscal scope version")
		}
	}
	if decoder.More() {
		return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "unknown binding field")
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "invalid binding object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return FiscalScopeBinding{}, scopeError(ScopeBindingInvalid, "trailing JSON value")
	}
	result := FiscalScopeBinding{
		Version:        values[0],
		Tenant:         values[1],
		Organization:   values[2],
		Company:        values[3],
		FiscalPeriod:   values[4],
		LedgerBook:     values[5],
		OperationType:  values[6],
		SourceSnapshot: values[7],
		PolicyVersion:  values[8],
		Actor:          values[9],
		AuthorityLevel: AuthorityLevel(values[10]),
	}
	if err := ValidateFiscalScopeBinding(result); err != nil {
		return FiscalScopeBinding{}, err
	}
	return result, nil
}

func ValidateFiscalScopeBinding(binding FiscalScopeBinding) error {
	if binding.Version == "" {
		return scopeError(ScopeBindingRequired, "binding version is required")
	}
	if binding.Version != "v1" {
		return scopeError(UnsupportedScopeVersion, "unsupported fiscal scope version")
	}
	for key, value := range map[string]string{
		"tenant": binding.Tenant, "organization": binding.Organization, "company": binding.Company,
		"fiscalPeriod": binding.FiscalPeriod, "ledgerBook": binding.LedgerBook,
		"operationType": binding.OperationType, "sourceSnapshot": binding.SourceSnapshot,
		"policyVersion": binding.PolicyVersion, "actor": binding.Actor, "authorityLevel": string(binding.AuthorityLevel),
	} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
			return scopeError(ScopeBindingInvalid, key+" is invalid")
		}
	}
	if !IsValidFiscalRUC(binding.Company) {
		return scopeError(ScopeBindingInvalid, "company is not a valid RUC")
	}
	if !IsValidPeriod(binding.FiscalPeriod) {
		return scopeError(ScopeBindingInvalid, "fiscalPeriod must be YYYYMM")
	}
	if len(binding.SourceSnapshot) != 64 {
		return scopeError(ScopeBindingInvalid, "sourceSnapshot must be lowercase sha256")
	}
	if _, err := hex.DecodeString(binding.SourceSnapshot); err != nil || strings.ToLower(binding.SourceSnapshot) != binding.SourceSnapshot {
		return scopeError(ScopeBindingInvalid, "sourceSnapshot must be lowercase sha256")
	}
	expected, ok := fiscalOperations[binding.OperationType]
	if !ok || expected != binding.AuthorityLevel {
		return scopeError(ScopeBindingInvalid, "operationType and authorityLevel do not match the v1 vocabulary")
	}
	return nil
}

func CanonicalFiscalScopeBytes(binding FiscalScopeBinding) []byte {
	values := [...]string{binding.Tenant, binding.Organization, binding.Company, binding.FiscalPeriod, binding.LedgerBook, binding.OperationType, binding.SourceSnapshot, binding.PolicyVersion, binding.Actor, string(binding.AuthorityLevel)}
	var out bytes.Buffer
	out.WriteString("drenyra:fiscal-scope:v1\x00")
	for i, value := range values {
		out.WriteString(fiscalScopeKeys[i+1])
		out.WriteByte('=')
		out.WriteString(strconv.Itoa(len([]byte(value))))
		out.WriteByte(':')
		out.WriteString(value)
		out.WriteByte(0)
	}
	return out.Bytes()
}

func FiscalScopeHash(binding FiscalScopeBinding) string {
	sum := sha256.Sum256(CanonicalFiscalScopeBytes(binding))
	return hex.EncodeToString(sum[:])
}

func scopeError(code, message string) error { return &FiscalScopeError{Code: code, Message: message} }
