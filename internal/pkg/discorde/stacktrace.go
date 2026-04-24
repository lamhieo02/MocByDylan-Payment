package discorde

import (
	"reflect"
	"runtime"
	"strings"
)

func extractPcs(method reflect.Value) []uintptr {
	var pcs []uintptr

	stacktrace := method.Call(make([]reflect.Value, 0))[0]
	if stacktrace.Kind() != reflect.Slice {
		return nil
	}

	for i := 0; i < stacktrace.Len(); i++ {
		pc := stacktrace.Index(i)

		switch pc.Kind() {
		case reflect.Uintptr:
			pcs = append(pcs, uintptr(pc.Uint()))
		case reflect.Struct:
			for _, fieldName := range []string{"ProgramCounter", "PC"} {
				field := pc.FieldByName(fieldName)
				if !field.IsValid() {
					continue
				}
				if field.Kind() == reflect.Uintptr {
					pcs = append(pcs, uintptr(field.Uint()))
					break
				}
			}
		}
	}

	return pcs
}

func filterFrames(frames []Frame) []Frame {
	if len(frames) == 0 {
		return nil
	}

	filteredFrames := make([]Frame, 0, len(frames))

	for _, frame := range frames {
		if frame.Module == "runtime" || frame.Module == "testing" {
			continue
		}
		if strings.HasPrefix(frame.Module, "github.com/mocbydylan/shopify-mocbydylan-payos-payment") &&
			!strings.HasSuffix(frame.Module, "_test") {
			continue
		}

		if strings.HasPrefix(frame.Filename, "/usr/local/go/") {
			continue
		}

		if strings.Contains(frame.Filename, "/go/pkg/mod") ||
			strings.Contains(frame.Filename, "/proto/") ||
			strings.Contains(frame.Filename, "/package/") ||
			strings.Contains(frame.Filename, "/pkg/discorde/") ||
			strings.Contains(frame.Filename, "/pkg/mgrpc/") ||
			strings.Contains(frame.Filename, "/pkg/xerror/") ||
			strings.Contains(frame.Filename, "/pkg/contxt/") ||
			strings.Contains(frame.Filename, "middleware") ||
			strings.Contains(frame.Function, "notifyErrorToDiscord") {

			continue
		}

		filteredFrames = append(filteredFrames, frame)
	}

	return filteredFrames
}

func ExtractFrames(pcs []uintptr) []Frame {
	var frames []Frame
	callersFrames := runtime.CallersFrames(pcs)

	for {
		callerFrame, more := callersFrames.Next()

		frames = append([]Frame{
			NewFrame(callerFrame),
		}, frames...)

		if !more {
			break
		}
	}

	return frames
}

func packageName(name string) string {
	// A prefix of "type." and "go." is a compiler-generated symbol that doesn't belong to any package.
	// See variable reservedimports in cmd/compile/internal/gc/subr.go
	if strings.HasPrefix(name, "go.") || strings.HasPrefix(name, "type.") {
		return ""
	}

	pathend := strings.LastIndex(name, "/")
	if pathend < 0 {
		pathend = 0
	}

	if i := strings.Index(name[pathend:], "."); i != -1 {
		return name[:pathend+i]
	}
	return ""
}

func splitQualifiedFunctionName(name string) (pkg string, fun string) {
	pkg = packageName(name)
	fun = strings.TrimPrefix(name, pkg+".")
	return
}

func NewFrame(f runtime.Frame) Frame {
	function := f.Function

	var pkg string
	if function != "" {
		pkg, function = splitQualifiedFunctionName(function)
	}

	frame := Frame{
		Function:       function,
		Module:         pkg,
		Filename:       f.File,
		Lineno:         f.Line,
		ProgramCounter: f.PC,
	}

	return frame
}

func ExtractStacktrace(exception error) *Stacktrace {
	var pcs []uintptr
	stackTrace := reflect.ValueOf(exception).MethodByName("StackTrace")
	if stackTrace.IsValid() {
		pcs = extractPcs(stackTrace)
	}

	if len(pcs) == 0 {
		return nil
	}

	frames := ExtractFrames(pcs)
	frames = filterFrames(frames)

	return &Stacktrace{
		Frames: frames,
	}
}

func NewStacktrace() *Stacktrace {
	pcs := make([]uintptr, 100)
	n := runtime.Callers(0, pcs)

	if n == 0 {
		return nil
	}

	frames := ExtractFrames(pcs[:n])
	frames = filterFrames(frames)

	stacktrace := Stacktrace{
		Frames: frames,
	}

	return &stacktrace
}
