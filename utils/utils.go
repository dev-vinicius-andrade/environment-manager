package utils

func Ternary(condition bool, a interface{}, b interface{}) interface{} {
	if condition {
		return a
	}
	return b
}
