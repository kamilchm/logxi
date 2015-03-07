package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-errors/errors"
	"github.com/mgutz/ansi"
	"gopkg.in/stack.v1"
)

// Theme defines a color theme for HappyDevFormatter
type colorScheme struct {
	Key    string
	Value  string
	Misc   string
	Source string

	Debug string
	Info  string
	Warn  string
	Error string
}

const assignmentChar = ": "

var indent = "  "
var maxCol = defaultMaxCol
var theme *colorScheme

func parseKVList(s, separator string) map[string]string {
	pairs := strings.Split(s, separator)
	if len(pairs) == 0 {
		return nil
	}
	m := map[string]string{}
	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		parts := strings.Split(pair, "=")
		switch len(parts) {
		case 1:
			m[parts[0]] = ""
		case 2:
			m[parts[0]] = parts[1]
		}
	}
	return m
}

func parseTheme(theme string) *colorScheme {
	m := parseKVList(theme, ",")
	cs := &colorScheme{}
	var wildcard string

	var color = func(key string) string {
		style := m[key]
		c := ansi.ColorCode(style)
		if c == "" {
			c = wildcard
		}
		//fmt.Printf("plain=%b [%s] %s=%q\n", ansi.Plain, key, style, c)
		return c
	}

	wildcard = color("*")

	if wildcard != ansi.Reset {
		cs.Key = wildcard
		cs.Value = wildcard
		cs.Misc = wildcard
		cs.Source = wildcard
		cs.Debug = wildcard
		cs.Warn = wildcard
		cs.Info = wildcard
		cs.Error = wildcard
	}

	cs.Key = color("key")
	cs.Value = color("value")
	cs.Misc = color("misc")
	cs.Source = color("source")

	cs.Debug = color("DBG")
	cs.Warn = color("WRN")
	cs.Info = color("INF")
	cs.Error = color("ERR")
	return cs
}

func keyColor(s string) string {
	return theme.Key + s + ansi.Reset
}

// DisableColors disables coloring of log entries.
func DisableColors(val bool) {
	disableColors = val
}

// HappyDevFormatter is the formatter used for terminals. It is
// colorful, dev friendly and provides meaningful logs when
// warnings and errors occur.
//
// HappyDevFormatter does not worry about performance. It's at least 3-4X
// slower than JSONFormatter since it delegates to JSONFormatter to marshal
// then unmarshal JSON. Then it does other stuff like read source files, sort
// keys all to give a developer more information.
//
// NEVER use in production.
type HappyDevFormatter struct {
	name string
	col  int
	// always use the production formatter
	jsonFormatter *JSONFormatter
}

// NewHappyDevFormatter returns a new instance of HappyDevFormatter.
func NewHappyDevFormatter(name string) *HappyDevFormatter {
	jf := NewJSONFormatter(name)
	jf.disableCallStack(true)
	return &HappyDevFormatter{
		name:          name,
		jsonFormatter: jf,
	}
}

func (hd *HappyDevFormatter) writeKey(buf *bytes.Buffer, key string) {
	// assumes this is not the first key
	hd.writeString(buf, Separator)
	if key == "" {
		return
	}
	buf.WriteString(theme.Key)
	hd.writeString(buf, key)
	hd.writeString(buf, assignmentChar)
	buf.WriteString(ansi.Reset)
}

func (hd *HappyDevFormatter) offset(buf *bytes.Buffer, color string, key string, value string) {
	val := strings.Trim(value, "\n ")

	if (isPretty && key != "") || hd.col+len(key)+1+len(val) >= maxCol {
		buf.WriteString("\n")
		hd.col = 0
		hd.writeString(buf, indent)
	}
	hd.writeKey(buf, key)
	if color != "" {
		buf.WriteString(color)
	}
	hd.writeString(buf, val)

	if color != "" {
		buf.WriteString(ansi.Reset)
	}
}

// writeError writes an error. It eventually calls offset which adds formatting
// newlines, etc.
func (hd *HappyDevFormatter) writeError(buf *bytes.Buffer, key string, err *errors.Error) {
	msg := err.Error()
	stack := string(err.Stack())
	hd.offset(buf, theme.Error, key, msg+"\n"+stack)
}

// set writes a key-value pair to buf. It eventually calls offset which adds
// formatting newlines, etc.
func (hd *HappyDevFormatter) set(buf *bytes.Buffer, key string, value interface{}, color string) {
	if err, ok := value.(error); ok {
		err2 := errors.Wrap(err, 4)
		hd.writeError(buf, key, err2)
	} else if err, ok := value.(*errors.Error); ok {
		hd.writeError(buf, key, err)
	} else {
		hd.offset(buf, color, key, fmt.Sprintf("%v", value))
	}
}

// tracks the position of the string so we can break lines cleanly. Do not
// send ANSI escape sequences, just raw strings
func (hd *HappyDevFormatter) writeString(buf *bytes.Buffer, s string) {
	buf.WriteString(s)
	hd.col += len(s)
}

func (hd *HappyDevFormatter) getLevelContext(level int) (context string, color string) {
	switch level {
	case LevelDebug:
		color = theme.Debug
	case LevelInfo:
		color = theme.Info
	case LevelWarn:
		trace := stack.Trace().TrimRuntime()
		// if one line, keep it on same line, multiple lines group all
		// on next line
		for i, stack := range trace {
			if i < 4 {
				continue
			}
			ci := newCallstackInfo(stack, -1)
			context = ci.String(theme.Warn, theme.Source)
			break
		}
		color = theme.Warn

	default:
		trace := stack.Trace().TrimRuntime()

		// if one line, keep it on same line, multiple lines group all
		// on next line
		var errbuf bytes.Buffer
		lines := 0
		for i, stack := range trace {
			if i < 3 {
				continue
			}
			ci := newCallstackInfo(stack, contextLines)
			ctx := ci.String(theme.Error, theme.Source)
			if ctx == "" {
				continue
			}
			errbuf.WriteString(ctx)
			errbuf.WriteRune('\n')
			lines++
		}
		context = errbuf.String()
		color = theme.Error
	}

	return context, color
}

// Format records a log entry.
func (hd *HappyDevFormatter) Format(buf *bytes.Buffer, level int, msg string, args []interface{}) {

	// warn about reserved, bad and complex keys
	for i := 0; i < len(args); i += 2 {
		isReserved, err := isReservedKey(args[i])
		if err != nil {
			InternalLog.Error("Key is not a string.", fmt.Sprintf("args[%d]", i), fmt.Sprintf("%v", args[i]))
		} else if isReserved {
			InternalLog.Fatal("Key conflicts with reserved key. Avoiding using single rune keys.", "key", args[i].(string))
		} else {
			// Ensure keys are simple strings. The JSONFormatter doesn't escape
			// keys as a performance tradeoff. This panics if the JSON key
			// value has a different value than a simple quoted string.
			key := args[i].(string)
			b, err := json.Marshal(key)
			if err != nil {
				panic("Key is invalid. " + err.Error())
			}
			if string(b) != `"`+key+`"` {
				panic("Key is complex. Use simpler key for: " + fmt.Sprintf("%q", key))
			}
		}

	}

	// use the production JSON formatter to format the log first. This
	// ensures JSON will marshal/unmarshal correctly in production.
	entry := hd.jsonFormatter.LogEntry(level, msg, args)

	// reset the column tracker used for fancy formatting
	hd.col = 0

	// timestamp
	buf.WriteString(theme.Misc)
	hd.writeString(buf, entry[timeKey].(string))
	buf.WriteString(ansi.Reset)

	// emphasize warnings and errors
	context, color := hd.getLevelContext(level)

	// DBG, INF ...
	hd.set(buf, "", entry[levelKey].(string), color)
	// logger name
	hd.set(buf, "", entry[nameKey], theme.Misc)
	// message from user
	hd.set(buf, "", entry[messageKey], color)

	// Preserve key order in the order developer added them
	// in the call. This makes it easier for developers to follow
	// the log.
	order := []string{}
	lenArgs := len(args)
	for i := 0; i < len(args); i += 2 {
		if i+1 >= lenArgs {
			continue
		}
		if key, ok := args[i].(string); ok {
			order = append(order, key)
		} else {
			order = append(order, badKeyAtIndex(i))
		}
	}

	for _, key := range order {
		// skip resetved keys which were set above
		isReserved, err := isReservedKey(key)
		if err != nil {
			panic("key is invalid. Should never get here. " + err.Error())
		} else if isReserved {
			continue
		}
		hd.set(buf, key, entry[key], theme.Value)
	}

	// WRN,ERR file, line number context
	if context != "" {
		buf.WriteRune('\n')
		buf.WriteString(color)
		buf.WriteString(context)
		buf.WriteString(ansi.Reset)
		if context[len(context)-1:] != "\n" {
			buf.WriteRune('\n')
		}
		//hd.set(buf, atKey, context, color)
	} else {
		buf.WriteRune('\n')
	}
}
