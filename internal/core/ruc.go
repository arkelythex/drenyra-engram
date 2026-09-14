package core

// RUCClassification is a stable, non-enumerating diagnostic for the canonical
// SUNAT modulo-11 validator. Validation does not query SUNAT or grant authority.
type RUCClassification string

const (
	RUCValid           RUCClassification = "valid"
	RUCInvalidShape    RUCClassification = "invalid_shape"
	RUCInvalidChecksum RUCClassification = "invalid_checksum"
)

var rucWeights = [...]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}

func ClassifyRUC(ruc string) RUCClassification {
	if len(ruc) != 11 {
		return RUCInvalidShape
	}
	sum := 0
	for i, weight := range rucWeights {
		if ruc[i] < '0' || ruc[i] > '9' {
			return RUCInvalidShape
		}
		sum += int(ruc[i]-'0') * weight
	}
	if ruc[10] < '0' || ruc[10] > '9' {
		return RUCInvalidShape
	}
	expected := 11 - sum%11
	if expected == 10 || expected == 11 {
		expected -= 10
	}
	if expected != int(ruc[10]-'0') {
		return RUCInvalidChecksum
	}
	return RUCValid
}

func IsValidFiscalRUC(ruc string) bool { return ClassifyRUC(ruc) == RUCValid }
