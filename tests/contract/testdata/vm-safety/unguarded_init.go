package vmtest

var unsafeInitialization = func() int {
	return 1
}()

func init() {}
