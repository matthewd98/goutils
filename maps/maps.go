package maps

func Equal[K, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}

	return true
}

func Copy[K comparable, V any](m map[K]V) map[K]V {
	result := make(map[K]V, len(m))
	for k, v := range m {
		result[k] = v
	}

	return result
}

// Map maps each key-value pair of the original map to a new key-value pair of a new map.
func Map[K1, K2 comparable, V1, V2 any](m map[K1]V1, fn func(k K1, v V1) (K2, V2)) map[K2]V2 {
	result := make(map[K2]V2, len(m))
	for k1, v1 := range m {
		k2, v2 := fn(k1, v1)
		result[k2] = v2
	}

	return result
}

// Merge merges two maps. If a key in the first map is present in the second, the value from the first is used.
func Merge[K comparable, V any](m1, m2 map[K]V) map[K]V {
	result := make(map[K]V, len(m1)+len(m2))
	for k, v := range m2 {
		result[k] = v
	}

	for k, v := range m1 {
		result[k] = v
	}

	return result
}

func ToSlice[K comparable, V, T any](m map[K]V, fn func(k K, v V) T) []T {
	result := make([]T, 0, len(m))
	for k, v := range m {
		result = append(result, fn(k, v))
	}

	return result
}

func Keys[K comparable, V any](m map[K]V) []K {
	return ToSlice(m, func(k K, _ V) K { return k })
}

func Values[K comparable, V any](m map[K]V) []V {
	return ToSlice(m, func(_ K, v V) V { return v })
}
