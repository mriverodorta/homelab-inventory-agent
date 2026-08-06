//go:build !linux && !freebsd

package inventoryscan

type unsupportedScanner struct{}

func NewScanner() Scanner { return unsupportedScanner{} }
