package maths

import (
	"golang.org/x/exp/constraints"
)

func Min[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func Max[T constraints.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func LessThan[T constraints.Ordered](a, b T) bool {
	return a < b
}

func GreaterThan[T constraints.Ordered](a, b T) bool {
	return a > b
}

func IsWithinRange[T constraints.Ordered](val, lowerBound, upperBound T) bool {
	return val >= lowerBound && val <= upperBound
}
