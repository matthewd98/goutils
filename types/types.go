package types

import (
	"errors"

	"golang.org/x/exp/constraints"
)

func ToPointer[T any](t T) *T {
	return &t
}

func ToStringFromBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func ToIntFromBool[T constraints.Integer](b bool) T {
	if b {
		return 1
	}
	return 0
}

func ToBoolFromInt[T constraints.Integer](i T) bool {
	return i > 0
}

func ErrorAs[T error](err error) (T, bool) {
	var oe T
	if errors.As(err, &oe) {
		return oe, true
	}
	return *new(T), false
}
