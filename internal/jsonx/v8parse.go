package jsonx

import (
	"strings"
	"unicode/utf16"
)

// 本文件复刻 V8（Node 24 的 JSON.parse）与 express.json 的请求体解析报错文案。
//
// 旧实现里请求体经 `express.json({limit:'64kb'})` 解析：先做 strict 首字符校验，再交给
// `JSON.parse`；两者抛出的 SyntaxError.message 会原样进入 500 响应体。为逐字对齐，
// 这里照抄 V8 json-parser 的扫描/报错逻辑（position/line/column 与 snippet 截断规则）。
//
// 位置一律按 UTF-16 码元计（V8 字符串即 UTF-16），line/column 从 1 起，`\r\n` 记一个换行。

// V8 源码中的上下文窗口常量（json-parser.h）。
const (
	maxContextCharacters              = 10
	minOriginalSourceLengthForContext = maxContextCharacters*2 + 1
)

// jsonToken 对应 V8 的 JsonToken 枚举（json-parser.h）。
type jsonToken uint8

const (
	tokNumber jsonToken = iota
	tokString
	tokLBrace
	tokRBrace
	tokLBrack
	tokRBrack
	tokTrue
	tokFalse
	tokNull
	tokWhitespace
	tokColon
	tokComma
	tokIllegal
	tokEOS
)

// escapeKind 对应 V8 的 EscapeKind 枚举。
type escapeKind uint8

const (
	escIllegal escapeKind = iota
	escSelf
	escBackspace
	escTab
	escNewLine
	escFormFeed
	escCarriageReturn
	escUnicode
)

func isDecimalDigit(c uint16) bool { return c >= '0' && c <= '9' }

// isNumberPart 对应 V8 的 IsNumberPart：数字可继续消费的字符集。
func isNumberPart(c uint16) bool {
	return c == '.' || c == 'e' || c == 'E' || isDecimalDigit(c) || c == '-' || c == '+'
}

// jsonTokenForChar 对应 V8 的 GetOneCharJsonToken（仅 0x00..0xFF 有专门归属）。
func jsonTokenForChar(c uint16) jsonToken {
	switch {
	case c == '"':
		return tokString
	case isDecimalDigit(c), c == '-':
		return tokNumber
	case c == '[':
		return tokLBrack
	case c == '{':
		return tokLBrace
	case c == ']':
		return tokRBrack
	case c == '}':
		return tokRBrace
	case c == 't':
		return tokTrue
	case c == 'f':
		return tokFalse
	case c == 'n':
		return tokNull
	case c == ' ', c == '\t', c == '\r', c == '\n':
		return tokWhitespace
	case c == ':':
		return tokColon
	case c == ',':
		return tokComma
	default:
		return tokIllegal
	}
}

// escapeKindFor 对应 V8 的 GetEscapeKind。
func escapeKindFor(c uint16) escapeKind {
	switch c {
	case 'b':
		return escBackspace
	case 't':
		return escTab
	case 'n':
		return escNewLine
	case 'f':
		return escFormFeed
	case 'r':
		return escCarriageReturn
	case 'u':
		return escUnicode
	case '"', '\\', '/':
		return escSelf
	default:
		return escIllegal
	}
}

// isJSONWhitespace 是 JSON 允许的空白（RFC 7159 / body-parser FIRST_CHAR_REGEXP）。
func isJSONWhitespace(c uint16) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// v8Parser 是 V8 JSON 解析器的报错子集：只用来看「是否合法」以及「不合法时的原文」。
type v8Parser struct {
	chars  []uint16
	pos    int
	next   jsonToken
	failed bool
	msg    string
}

// BodyJSONError 复刻旧实现请求体解析失败时的 error 文案。
//
// 返回 (消息, true) 表示解析失败；("", false) 表示是合法 JSON（可继续按对象/数组取字段）。
// 逻辑对齐 body-parser：空体 → 成功；strict 首字符非 `{`/`[` → createStrictSyntaxError；
// 否则走 JSON.parse 的原始报错。
func BodyJSONError(src string) (string, bool) {
	units := utf16.Encode([]rune(src))
	if len(units) == 0 {
		return "", false
	}
	if first, idx, ok := firstNonWhitespace(units); !ok || (first != '{' && first != '[') {
		return strictSyntaxError(src, units, idx), true
	}
	if p := newV8Parser(units); !p.parse() {
		return p.msg, true
	}
	return "", false
}

// V8ParseError 返回 JSON.parse(src) 的原始报错文案；仅应在 src 非法时调用。
func V8ParseError(src string) string {
	p := newV8Parser(utf16.Encode([]rune(src)))
	if p.parse() {
		return ""
	}
	return p.msg
}

func newV8Parser(units []uint16) *v8Parser { return &v8Parser{chars: units} }

// firstNonWhitespace 返回首个非空白码元及其下标（全空白时 ok=false、idx=-1）。
func firstNonWhitespace(units []uint16) (uint16, int, bool) {
	for i, c := range units {
		if !isJSONWhitespace(c) {
			return c, i, true
		}
	}
	return 0, -1, false
}

// strictSyntaxError 复刻 body-parser 的 createStrictSyntaxError：把首个非空白字符起的位置
// 整体替换为 `#` 后再交给 JSON.parse，最后把消息里的 `#+` 回填为原文片段。
func strictSyntaxError(src string, units []uint16, index int) string {
	var partial string
	if index >= 0 {
		run := make([]uint16, 0, len(units))
		run = append(run, units[:index]...)
		for i := index; i < len(units); i++ {
			run = append(run, '#')
		}
		partial = unitsToString(run)
	}
	msg := V8ParseError(partial)
	return expandHashes(msg, units, index)
}

// expandHashes 把消息里每一段 `#` 回填为原文从 index 起的等长片段（body-parser 的 replace）。
func expandHashes(msg string, units []uint16, index int) string {
	var b strings.Builder
	for i := 0; i < len(msg); {
		if msg[i] != '#' {
			b.WriteByte(msg[i])
			i++
			continue
		}
		j := i
		for j < len(msg) && msg[j] == '#' {
			j++
		}
		b.WriteString(unitsSubstring(units, index, index+(j-i)))
		i = j
	}
	return b.String()
}

// unitsSubstring 取 [start,end) 码元，越界按 JS String.prototype.substring 口径收敛。
func unitsSubstring(units []uint16, start, end int) string {
	if start < 0 {
		start = 0
	}
	if start > len(units) {
		start = len(units)
	}
	if end > len(units) {
		end = len(units)
	}
	if end < start {
		end = start
	}
	return unitsToString(units[start:end])
}

// unitsToString 把 UTF-16 码元序列编码为字符串：成对代理项合并为补充平面字符；孤立代理项按
// WTF-8 原样编出 3 字节（不能退化成 U+FFFD），好让 jsonx.Quote 再把它转义回 \uXXXX，从而与
// V8 报错文案经 JSON.stringify 序列化后的字节一致。
func unitsToString(units []uint16) string {
	var b strings.Builder
	b.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) {
			if lo := units[i+1]; lo >= 0xDC00 && lo <= 0xDFFF {
				b.WriteRune(utf16.DecodeRune(rune(u), rune(lo)))
				i++
				continue
			}
		}
		writeUTF16Unit(&b, u)
	}
	return b.String()
}

// writeUTF16Unit 按 UTF-8 写入单个码元；代理项区间写 WTF-8（3 字节）以保留孤立代理项。
func writeUTF16Unit(b *strings.Builder, u uint16) {
	switch {
	case u < 0x80:
		b.WriteByte(byte(u))
	case u < 0x800:
		b.WriteByte(byte(0xC0 | u>>6))
		b.WriteByte(byte(0x80 | u&0x3F))
	default:
		b.WriteByte(byte(0xE0 | u>>12))
		b.WriteByte(byte(0x80 | (u>>6)&0x3F))
		b.WriteByte(byte(0x80 | u&0x3F))
	}
}

// --- 解析状态机 ---

func (p *v8Parser) isAtEnd() bool { return p.pos >= len(p.chars) }

// currentCharacter 对应 V8 CurrentCharacter：越界返回 kEndOfString(-1)。
func (p *v8Parser) currentCharacter() int {
	if p.isAtEnd() {
		return -1
	}
	return int(p.chars[p.pos])
}

func (p *v8Parser) advance() { p.pos++ }

// nextCharacter 对应 V8 NextCharacter：先前进再返回当前字符。
func (p *v8Parser) nextCharacter() int {
	p.advance()
	return p.currentCharacter()
}

// getNextNonWhitespaceToken 对应 V8 GetNextNonWhitespaceToken。
func (p *v8Parser) getNextNonWhitespaceToken() {
	for p.pos < len(p.chars) && jsonTokenForChar(p.chars[p.pos]) == tokWhitespace {
		p.pos++
	}
	if p.isAtEnd() {
		p.next = tokEOS
		return
	}
	p.next = jsonTokenForChar(p.chars[p.pos])
}

func (p *v8Parser) peek() jsonToken { return p.next }

// isNextToken 对应 V8 IsNextToken：仅对字符类 token 看当前字符，EOS 看是否到末尾。
func (p *v8Parser) isNextToken(tok jsonToken) bool {
	if tok == tokEOS {
		return p.isAtEnd()
	}
	var want int
	switch tok {
	case tokString:
		want = '"'
	case tokLBrace:
		want = '{'
	case tokRBrace:
		want = '}'
	case tokLBrack:
		want = '['
	case tokRBrack:
		want = ']'
	case tokTrue:
		want = 't'
	case tokFalse:
		want = 'f'
	case tokNull:
		want = 'n'
	case tokColon:
		want = ':'
	case tokComma:
		want = ','
	default:
		return false
	}
	return p.currentCharacter() == want
}

// check 对应 V8 Check<token>：先看当前字符，必要时跳过空白再比一次。
func (p *v8Parser) check(tok jsonToken) bool {
	if p.isNextToken(tok) {
		p.advance()
		return true
	}
	p.getNextNonWhitespaceToken()
	if p.peek() == tok {
		p.advance()
		return true
	}
	return false
}

// expect 对应 V8 Expect<token>：不跳空白，直接比对已扫描的 token。
func (p *v8Parser) expect(tok jsonToken, phrase string) bool {
	if p.peek() == tok {
		p.advance()
		return true
	}
	p.reportAt(p.peek(), phrase)
	return false
}

// expectNext 对应 V8 ExpectNext<token>：先 Check（可能跳空白）再比对。
func (p *v8Parser) expectNext(tok jsonToken, phrase string) bool {
	if p.check(tok) {
		return true
	}
	p.reportAt(p.peek(), phrase)
	return false
}

func (p *v8Parser) consume(tok jsonToken) {
	if p.peek() == tok {
		p.advance()
	}
}

// --- 报错渲染 ---

// reportAt 渲染「<短语> in JSON at position P (line L column C)」。
func (p *v8Parser) reportAt(_ jsonToken, phrase string) {
	line, col := fileLocation(p.pos, p.chars)
	p.fail(phrase + " in JSON at position " + itoa(p.pos) + " (line " + itoa(line) + " column " + itoa(col) + ")")
}

func (p *v8Parser) fail(msg string) {
	if p.failed {
		return
	}
	p.msg = msg
	p.failed = true
	p.pos = len(p.chars)
}

// reportUnexpectedToken 对应 V8 ReportUnexpectedToken：有指定短语用短语，否则按 token 归位。
func (p *v8Parser) reportUnexpectedToken(tok jsonToken, phrase string) {
	if p.failed {
		return
	}
	if phrase != "" {
		p.reportAt(tok, phrase)
		return
	}
	switch tok {
	case tokEOS:
		p.fail("Unexpected end of JSON input")
	case tokNumber:
		p.reportAt(tok, "Unexpected number")
	case tokString:
		p.reportAt(tok, "Unexpected string")
	default:
		if p.isSpecialString() {
			p.fail("\"" + unitsToString(p.chars) + "\" is not valid JSON")
			return
		}
		p.reportEllipses()
	}
}

// reportUnexpectedCharacter 对应 V8 ReportUnexpectedCharacter。
func (p *v8Parser) reportUnexpectedCharacter(c int) {
	tok := tokIllegal
	switch {
	case c == -1:
		tok = tokEOS
	case c <= 0xFF:
		tok = jsonTokenForChar(uint16(c))
	}
	p.reportUnexpectedToken(tok, "")
}

// reportEllipses 对应 V8 GetErrorMessageWithEllipses：token 字符 + 上下文片段。
func (p *v8Parser) reportEllipses() {
	pos := p.pos
	n := len(p.chars)
	tokChar := ""
	if pos < n {
		tokChar = unitsToString(p.chars[pos : pos+1])
	}
	if n < minOriginalSourceLengthForContext {
		p.fail("Unexpected token '" + tokChar + "', \"" + unitsToString(p.chars) + "\" is not valid JSON")
		return
	}
	start, end := 0, n
	pre, post := "", ""
	switch {
	case pos < maxContextCharacters:
		end = pos + maxContextCharacters
		post = "..."
	case pos >= maxContextCharacters && pos < n-maxContextCharacters:
		start = pos - maxContextCharacters
		end = pos + maxContextCharacters
		pre, post = "...", "..."
	default:
		start = pos - maxContextCharacters
		pre = "..."
	}
	snippet := unitsToString(p.chars[start:end])
	p.fail("Unexpected token '" + tokChar + "', " + pre + "\"" + snippet + "\"" + post + " is not valid JSON")
}

// isSpecialString 对应 V8 IsSpecialString：整串恰为这几种时用裸串文案。
func (p *v8Parser) isSpecialString() bool {
	switch unitsToString(p.chars) {
	case "[object Object]", "undefined", "Infinity", "NaN":
		return true
	default:
		return false
	}
}

// fileLocation 对应 V8 CalculateFileLocation（1 起，`\r\n` 记一个换行）。
func fileLocation(pos int, chars []uint16) (int, int) {
	line := 1
	lastLineBreak := 0
	for i := 0; i < pos; {
		if chars[i] == '\r' && i < pos-1 && chars[i+1] == '\n' {
			i++
		}
		if chars[i] == '\r' || chars[i] == '\n' {
			line++
			lastLineBreak = i + 1
		}
		i++
	}
	return line, 1 + (pos - lastLineBreak)
}

// --- 语法解析 ---

// parse 对应 JsonParser::ParseJson：解析一个值后要求以空白收尾。
func (p *v8Parser) parse() bool {
	p.parseValue()
	if p.failed {
		return false
	}
	if !p.check(tokEOS) {
		p.reportNonWhitespace()
		return false
	}
	return true
}

// reportNonWhitespace 渲染 kJsonParseUnexpectedNonWhiteSpaceCharacter（该模板不含 "in JSON"）。
func (p *v8Parser) reportNonWhitespace() {
	line, col := fileLocation(p.pos, p.chars)
	p.fail("Unexpected non-whitespace character after JSON at position " + itoa(p.pos) +
		" (line " + itoa(line) + " column " + itoa(col) + ")")
}

// parseValue 对应 ParseJsonValueRecursive。
func (p *v8Parser) parseValue() {
	p.getNextNonWhitespaceToken()
	switch p.peek() {
	case tokNumber:
		p.parseNumber()
	case tokString:
		p.consume(tokString)
		p.scanString()
	case tokTrue:
		p.scanLiteral("true")
	case tokFalse:
		p.scanLiteral("false")
	case tokNull:
		p.scanLiteral("null")
	case tokLBrace:
		p.parseObject()
	case tokLBrack:
		p.parseArray()
	default:
		p.reportUnexpectedCharacter(p.currentCharacter())
	}
}

// parseObject 对应 ParseJsonObject + ParseJsonObjectProperties<kJsonSlow>。
func (p *v8Parser) parseObject() {
	p.consume(tokLBrace)
	if p.check(tokRBrace) {
		return
	}
	phrase := "Expected property name or '}'"
	for {
		if !p.expectNext(tokString, phrase) {
			return
		}
		phrase = "Expected double-quoted property name"
		p.scanString()
		if p.failed {
			return
		}
		if !p.expectNext(tokColon, "Expected ':' after property name") {
			return
		}
		p.parseValue()
		if p.failed {
			return
		}
		if !p.check(tokComma) {
			break
		}
	}
	p.expect(tokRBrace, "Expected ',' or '}' after property value")
}

// parseArray 对应 ParseJsonArray（数字快路径与通用路径的报错一致，这里只保留后者）。
func (p *v8Parser) parseArray() {
	p.consume(tokLBrack)
	if p.check(tokRBrack) {
		return
	}
	for {
		p.parseValue()
		if p.failed {
			return
		}
		if !p.check(tokComma) {
			break
		}
	}
	p.expect(tokRBrack, "Expected ',' or ']' after array element")
}

// parseNumber 对应 ParseJsonNumberAsDoubleOrSmi 的报错分支。
func (p *v8Parser) parseNumber() {
	sign := 1
	c := p.currentCharacter()
	if c == '-' {
		sign = -1
		c = p.nextCharacter()
	}
	if c == '0' {
		c = p.nextCharacter()
		if c >= 0 && c <= 0xFF && isNumberPart(uint16(c)) {
			if isDecimalDigit(uint16(c)) {
				p.reportUnexpectedToken(tokNumber, "")
				return
			}
		} else if sign > 0 {
			return
		}
	} else {
		start := p.pos
		stop := p.pos + 9
		if stop > len(p.chars) {
			stop = len(p.chars)
		}
		for p.pos < stop && isDecimalDigit(p.chars[p.pos]) {
			p.pos++
		}
		if p.pos == start {
			p.reportUnexpectedToken(tokIllegal, "No number after minus sign")
			return
		}
		c = p.currentCharacter()
		if !(c >= 0 && c <= 0xFF && isNumberPart(uint16(c))) {
			return
		}
		p.advanceToNonDecimal()
	}
	if p.currentCharacter() == '.' {
		c = p.nextCharacter()
		if !(c >= 0 && isDecimalDigit(uint16(c))) {
			p.reportUnexpectedToken(tokIllegal, "Unterminated fractional number")
			return
		}
		p.advanceToNonDecimal()
	}
	if asciiLower(p.currentCharacter()) == 'e' {
		c = p.nextCharacter()
		if c == '-' || c == '+' {
			c = p.nextCharacter()
		}
		if !(c >= 0 && isDecimalDigit(uint16(c))) {
			p.reportUnexpectedToken(tokIllegal, "Exponent part is missing a number")
			return
		}
		p.advanceToNonDecimal()
	}
}

func (p *v8Parser) advanceToNonDecimal() {
	for p.pos < len(p.chars) && isDecimalDigit(p.chars[p.pos]) {
		p.pos++
	}
}

// scanLiteral 对应 V8 ScanLiteral：比较 true/false/null 的剩余字符。
func (p *v8Parser) scanLiteral(lit string) {
	n := len(lit)
	remaining := len(p.chars) - p.pos
	if remaining >= n && p.compareChars(lit[1:], p.pos+1) {
		p.pos += n
		return
	}
	p.pos++
	limit := n - 1
	if remaining-1 < limit {
		limit = remaining - 1
	}
	for i := 0; i < limit; i++ {
		c := p.currentCharacter()
		if c < 0 || lit[1+i] != byte(c) {
			p.reportUnexpectedCharacter(c)
			return
		}
		p.pos++
	}
	p.reportUnexpectedToken(tokEOS, "")
}

func (p *v8Parser) compareChars(s string, start int) bool {
	if start < 0 || start+len(s) > len(p.chars) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if p.chars[start+i] != uint16(s[i]) {
			return false
		}
	}
	return true
}

// scanString 对应 ScanJsonString：只关心报错，不构建字符串值。
func (p *v8Parser) scanString() {
	for {
		for p.pos < len(p.chars) {
			c := p.chars[p.pos]
			if c < 0x20 || c == '"' || c == '\\' {
				break
			}
			p.pos++
		}
		if p.isAtEnd() {
			p.reportUnexpectedToken(tokIllegal, "Unterminated string")
			return
		}
		switch p.chars[p.pos] {
		case '"':
			p.advance()
			return
		case '\\':
			c := p.nextCharacter()
			if c < 0 || c > 0xFF {
				p.reportUnexpectedCharacter(c)
				return
			}
			switch escapeKindFor(uint16(c)) {
			case escSelf, escBackspace, escTab, escNewLine, escFormFeed, escCarriageReturn:
				// 合法单字符转义，随后的 advance 收尾
			case escUnicode:
				if !p.scanUnicodeCharacter() {
					p.reportUnexpectedToken(tokIllegal, "Bad Unicode escape")
					return
				}
			case escIllegal:
				p.reportUnexpectedToken(tokIllegal, "Bad escaped character")
				return
			}
			p.advance()
		default:
			p.reportUnexpectedToken(tokIllegal, "Bad control character in string literal")
			return
		}
	}
}

// scanUnicodeCharacter 对应 V8 ScanUnicodeCharacter：读 4 位十六进制。
func (p *v8Parser) scanUnicodeCharacter() bool {
	value := 0
	for i := 0; i < 4; i++ {
		d := hexValue(p.nextCharacter())
		if d < 0 {
			return false
		}
		value = value*16 + d
	}
	return true
}

func hexValue(c int) int {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return -1
	}
}

func asciiLower(c int) int {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// itoa 是本地十进制转换（避免引入额外格式化依赖）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
