package jsonwire

import (
	"bytes"
	"math"
	"strconv"
)

// Decimal is a normalized arbitrary-precision JSON number. A non-zero value is
// significand * 10^scale; both components are stored as bounded decimal text.
type Decimal struct {
	negative       bool
	zero           bool
	significand    []byte
	scaleNegative  bool
	scaleMagnitude []byte
}

// ParseDecimal validates JSON number grammar and normalizes without converting
// the exponent or coefficient to a machine integer.
func ParseDecimal(raw []byte) (Decimal, error) {
	if len(raw) == 0 {
		return Decimal{}, newValidationError(KindSyntax, 0)
	}
	position := 0
	negative := false
	if raw[position] == '-' {
		negative = true
		position++
		if position == len(raw) {
			return Decimal{}, newValidationError(KindSyntax, position)
		}
	}

	integerStart := position
	if raw[position] == '0' {
		position++
	} else {
		if raw[position] < '1' || raw[position] > '9' {
			return Decimal{}, newValidationError(KindSyntax, position)
		}
		for position < len(raw) && isDecimalDigit(raw[position]) {
			position++
		}
	}
	integerEnd := position

	fractionStart := position
	fractionEnd := position
	if position < len(raw) && raw[position] == '.' {
		position++
		fractionStart = position
		if position == len(raw) || !isDecimalDigit(raw[position]) {
			return Decimal{}, newValidationError(KindSyntax, position)
		}
		for position < len(raw) && isDecimalDigit(raw[position]) {
			position++
		}
		fractionEnd = position
	}

	exponentNegative := false
	exponentMagnitude := []byte{'0'}
	if position < len(raw) && (raw[position] == 'e' || raw[position] == 'E') {
		position++
		if position < len(raw) && (raw[position] == '+' || raw[position] == '-') {
			exponentNegative = raw[position] == '-'
			position++
		}
		exponentStart := position
		if position == len(raw) || !isDecimalDigit(raw[position]) {
			return Decimal{}, newValidationError(KindSyntax, position)
		}
		for position < len(raw) && isDecimalDigit(raw[position]) {
			position++
		}
		exponentMagnitude = normalizeMagnitude(raw[exponentStart:position])
	}
	if position != len(raw) {
		return Decimal{}, newValidationError(KindSyntax, position)
	}

	coefficient := make([]byte, 0, integerEnd-integerStart+fractionEnd-fractionStart)
	coefficient = append(coefficient, raw[integerStart:integerEnd]...)
	coefficient = append(coefficient, raw[fractionStart:fractionEnd]...)
	first := 0
	for first < len(coefficient) && coefficient[first] == '0' {
		first++
	}
	if first == len(coefficient) {
		return Decimal{zero: true}, nil
	}
	last := len(coefficient)
	for last > first && coefficient[last-1] == '0' {
		last--
	}
	trailingZeros := len(coefficient) - last

	scaleNegative, scaleMagnitude := addSignedMagnitude(
		exponentNegative,
		exponentMagnitude,
		true,
		magnitudeFromInt(fractionEnd-fractionStart),
	)
	scaleNegative, scaleMagnitude = addSignedMagnitude(
		scaleNegative,
		scaleMagnitude,
		false,
		magnitudeFromInt(trailingZeros),
	)

	return Decimal{
		negative:       negative,
		significand:    append([]byte(nil), coefficient[first:last]...),
		scaleNegative:  scaleNegative,
		scaleMagnitude: scaleMagnitude,
	}, nil
}

// IsInteger reports mathematical integrality after normalization.
func (decimal Decimal) IsInteger() bool {
	return decimal.zero || len(decimal.significand) != 0 && !decimal.scaleNegative
}

// CompareUint64 compares the mathematical decimal value with an unsigned
// integer and returns -1, 0, or 1.
func (decimal Decimal) CompareUint64(value uint64) int {
	if decimal.zero {
		if value == 0 {
			return 0
		}
		return -1
	}
	if decimal.negative {
		return -1
	}
	if len(decimal.significand) == 0 || len(decimal.scaleMagnitude) == 0 {
		return -1
	}

	target := strconv.FormatUint(value, 10)
	if !decimal.scaleNegative {
		if len(decimal.significand) > len(target) {
			return 1
		}
		neededScale := len(target) - len(decimal.significand)
		switch compareMagnitudeInt(decimal.scaleMagnitude, neededScale) {
		case -1:
			return -1
		case 1:
			return 1
		}
		for index := range len(target) {
			digit := byte('0')
			if index < len(decimal.significand) {
				digit = decimal.significand[index]
			}
			if digit < target[index] {
				return -1
			}
			if digit > target[index] {
				return 1
			}
		}
		return 0
	}

	if value == 0 {
		return 1
	}
	if compareMagnitudeInt(decimal.scaleMagnitude, len(decimal.significand)) >= 0 {
		return -1
	}
	fractionDigits := magnitudeToSmallInt(decimal.scaleMagnitude)
	integerDigits := len(decimal.significand) - fractionDigits
	if integerDigits < len(target) {
		return -1
	}
	if integerDigits > len(target) {
		return 1
	}
	for index := range integerDigits {
		if decimal.significand[index] < target[index] {
			return -1
		}
		if decimal.significand[index] > target[index] {
			return 1
		}
	}
	// A normalized negative scale always leaves a non-zero fractional tail.
	return 1
}

// Uint64 converts only after mathematical integrality and range are proven.
func (decimal Decimal) Uint64() (uint64, bool) {
	if decimal.zero {
		return 0, true
	}
	if decimal.negative || !decimal.IsInteger() || decimal.CompareUint64(math.MaxUint64) > 0 {
		return 0, false
	}
	zeroCount := magnitudeToSmallInt(decimal.scaleMagnitude)
	digits := make([]byte, len(decimal.significand)+zeroCount)
	copy(digits, decimal.significand)
	for index := len(decimal.significand); index < len(digits); index++ {
		digits[index] = '0'
	}
	value, err := strconv.ParseUint(string(digits), 10, 64)
	return value, err == nil
}

// SemanticKey appends the canonical numeric RequestId key.
func (decimal Decimal) SemanticKey(dst []byte) ([]byte, error) {
	if decimal.zero {
		return append(dst, 'n', 0), nil
	}
	if len(decimal.significand) == 0 || len(decimal.scaleMagnitude) == 0 {
		return nil, newValidationError(KindMismatch, 0)
	}
	if len(decimal.significand) > math.MaxUint16 {
		return nil, newValidationError(KindResource, 0)
	}
	class := byte(1)
	if decimal.negative {
		class = 2
	}
	scaleSign := byte(0)
	if decimal.scaleNegative {
		scaleSign = 1
	}
	length := len(decimal.significand)
	dst = append(dst, 'n', class, byte(length>>8), byte(length))
	dst = append(dst, decimal.significand...)
	dst = append(dst, scaleSign)
	dst = append(dst, decimal.scaleMagnitude...)
	return dst, nil
}

func isDecimalDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func normalizeMagnitude(raw []byte) []byte {
	first := 0
	for first < len(raw) && raw[first] == '0' {
		first++
	}
	if first == len(raw) {
		return []byte{'0'}
	}
	return append([]byte(nil), raw[first:]...)
}

func magnitudeFromInt(value int) []byte {
	return strconv.AppendUint(nil, uint64(value), 10)
}

func addSignedMagnitude(leftNegative bool, left []byte, rightNegative bool, right []byte) (bool, []byte) {
	leftZero := magnitudeIsZero(left)
	rightZero := magnitudeIsZero(right)
	if leftZero && rightZero {
		return false, []byte{'0'}
	}
	if leftZero {
		return rightNegative, append([]byte(nil), right...)
	}
	if rightZero {
		return leftNegative, append([]byte(nil), left...)
	}
	if leftNegative == rightNegative {
		return leftNegative, addMagnitude(left, right)
	}
	switch compareMagnitude(left, right) {
	case 0:
		return false, []byte{'0'}
	case 1:
		return leftNegative, subtractMagnitude(left, right)
	default:
		return rightNegative, subtractMagnitude(right, left)
	}
}

func magnitudeIsZero(value []byte) bool {
	return len(value) == 1 && value[0] == '0'
}

func compareMagnitude(left, right []byte) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return bytes.Compare(left, right)
}

func addMagnitude(left, right []byte) []byte {
	width := max(len(left), len(right))
	result := make([]byte, width+1)
	leftPosition := len(left) - 1
	rightPosition := len(right) - 1
	carry := 0
	for position := width; position > 0; position-- {
		sum := carry
		if leftPosition >= 0 {
			sum += int(left[leftPosition] - '0')
			leftPosition--
		}
		if rightPosition >= 0 {
			sum += int(right[rightPosition] - '0')
			rightPosition--
		}
		result[position] = byte(sum%10) + '0'
		carry = sum / 10
	}
	if carry == 0 {
		return result[1:]
	}
	result[0] = byte(carry) + '0'
	return result
}

func subtractMagnitude(larger, smaller []byte) []byte {
	result := make([]byte, len(larger))
	smallerPosition := len(smaller) - 1
	borrow := 0
	for position := len(larger) - 1; position >= 0; position-- {
		difference := int(larger[position]-'0') - borrow
		if smallerPosition >= 0 {
			difference -= int(smaller[smallerPosition] - '0')
			smallerPosition--
		}
		if difference < 0 {
			difference += 10
			borrow = 1
		} else {
			borrow = 0
		}
		result[position] = byte(difference) + '0'
	}
	return normalizeMagnitude(result)
}

func compareMagnitudeInt(magnitude []byte, value int) int {
	return compareMagnitude(magnitude, magnitudeFromInt(value))
}

func magnitudeToSmallInt(magnitude []byte) int {
	value := 0
	for _, digit := range magnitude {
		value = value*10 + int(digit-'0')
	}
	return value
}
