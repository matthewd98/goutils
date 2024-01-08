package slices

import (
	"goutils/maths"

	"golang.org/x/exp/constraints"
)

// NilOr returns nil if target has no element. It is for the pre-allocated slice that needs to be assigned to another slice.
func NilOr[T any](items []T) []T {
	if len(items) == 0 {
		return nil
	}
	return items
}

// Contains verifies if a slice contains the target element.
func Contains[T comparable](items []T, target T) bool {
	for _, v := range items {
		if v == target {
			return true
		}
	}

	return false
}

// NonZeroValues returns a new slice with only non-zero values (e.g. non-nil pointers, non-empty strings, etc.) by preserving the original order.
func NonZeroValues[T comparable](items []T) []T {
	var zero T // nil for pointers, 0 for int, "" for string, etc.
	return Filter(items, func(item T) bool {
		return item != zero
	})
}

// Max returns an item with the maximum value.
func Max[T constraints.Ordered](items []T) T {
	return extremum(items, maths.GreaterThan[T])
}

// Min returns an item with the minimum value.
func Min[T constraints.Ordered](items []T) T {
	return extremum(items, maths.LessThan[T])
}

// extremum returns an slice item that meets the predicate accumulatively.
// If the slice is empty, return zero value.
func extremum[T constraints.Ordered](items []T, predicate func(T, T) bool) T {
	var zero T
	if len(items) == 0 {
		return zero
	}
	result := items[0]
	for _, item := range items {
		if predicate(item, result) {
			result = item
		}
	}

	return result
}

// Unique returns a new slice with only unique values. The ordering is preserved.
func Unique[T comparable](items []T) []T {
	seen := make(map[T]struct{}, len(items))
	result := make([]T, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}

	return result
}

func Map[T1, T2 any](items []T1, fn func(T1) T2) []T2 {
	result := make([]T2, 0, len(items))
	for _, item := range items {
		result = append(result, fn(item))
	}

	return result
}

// Filter keeps all items that meet predicate from the slice.
func Filter[T any](items []T, predicate func(T) bool) []T {
	result := make([]T, 0, len(items))
	for _, item := range items {
		if predicate(item) {
			result = append(result, item)
		}
	}

	return result
}

// ForEach applies a side-effect on each element in the slice.
func ForEach[T any](items []T, fn func(T)) {
	for _, item := range items {
		fn(item)
	}
}

// Any checks if a slice contains any element that meets predicate.
func Any[T any](items []T, predicate func(T) bool) bool {
	for _, item := range items {
		if predicate(item) {
			return true
		}
	}
	return false
}

// Repeat creates a slice from a value that is inserted N times.
func Repeat[T any](value T, times int) []T {
	result := make([]T, 0, times)
	for i := 0; i < times; i++ {
		result = append(result, value)
	}
	return result
}

// GroupBy groups the elements of a slice by the chosen keys.
func GroupBy[T any, K comparable](items []T, fn func(item T) K) map[K][]T {
	result := map[K][]T{}
	for _, item := range items {
		key := fn(item)
		result[key] = append(result[key], item)
	}

	return result
}

// ToMap returns a map with the key-value pair generated from each slice element.
func ToMap[T any, K comparable, V any](items []T, fn func(item T) (K, V)) map[K]V {
	result := make(map[K]V, len(items))
	for _, item := range items {
		k, v := fn(item)
		result[k] = v
	}

	return result
}

func Copy[T any](items []T) []T {
	result := make([]T, 0, len(items))
	return append(result, items...)
}

// Reduce reduces a slice to a value by running each element through an accumulator function.
// Each iteration is supplied the return value of the previous, except for the first iteration, which begins with a user-supplied initial value.
func Reduce[T any, R any](items []T, initial R, accumulator func(acc R, item T) R) R {
	for _, item := range items {
		initial = accumulator(initial, item)
	}

	return initial
}

// Flatten returns an array a single level deep
func Flatten[T any](items [][]T) []T {
	count := 0
	for i := range items {
		count += len(items[i])
	}

	result := make([]T, 0, count)
	for i := range items {
		result = append(result, items[i]...)
	}

	return result
}
