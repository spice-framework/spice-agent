package compaction

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
)

const (
	// ContractVersion changes whenever the compaction algorithm or summary
	// representation changes semantically.
	ContractVersion = "spice.agent.compaction/extractive-v1"

	minimumTriggerBytes   = 256
	maximumTriggerBytes   = 32 << 20
	minimumSummaryBytes   = 512
	maximumSummaryBytes   = 1 << 20
	maximumRetainedRounds = 4096
	maximumIdentityBytes  = 256
)

// Options is the complete semantic configuration of one compactor. A request
// is eligible only above TriggerBytes. The newest RetainRecentRounds complete
// tool rounds remain byte-for-byte intact, and the deterministic extract is
// bounded by MaximumSummaryBytes.
type Options struct {
	TriggerBytes        int
	RetainRecentRounds  int
	MaximumSummaryBytes int
}

// DefaultOptions returns conservative explicit defaults.
func DefaultOptions() Options {
	return Options{TriggerBytes: 4 << 20, RetainRecentRounds: 2, MaximumSummaryBytes: 256 << 10}
}

// Validate rejects partial and unbounded configuration.
func (options Options) Validate() error {
	if options.TriggerBytes < minimumTriggerBytes || options.TriggerBytes > maximumTriggerBytes {
		return fmt.Errorf("compaction trigger bytes must be between %d and %d", minimumTriggerBytes, maximumTriggerBytes)
	}
	if options.RetainRecentRounds < 0 || options.RetainRecentRounds > maximumRetainedRounds {
		return fmt.Errorf("compaction retained rounds must be between 0 and %d", maximumRetainedRounds)
	}
	if options.MaximumSummaryBytes < minimumSummaryBytes || options.MaximumSummaryBytes > maximumSummaryBytes {
		return fmt.Errorf("compaction summary bytes must be between %d and %d", minimumSummaryBytes, maximumSummaryBytes)
	}
	return nil
}

// Report contains content-free deterministic facts about one rewrite.
type Report struct {
	Compacted    bool
	Rounds       int
	Messages     int
	InputBytes   int
	OutputBytes  int
	SourceDigest string
}

// Provider is an explicit application-owned model.Provider wrapper. It is
// stateless and safe for concurrent use when its delegate is safe.
type Provider struct {
	delegate model.Provider
	options  Options
}

// NewProvider validates and constructs one deterministic wrapper.
func NewProvider(delegate model.Provider, options Options) (*Provider, error) {
	if delegate == nil {
		return nil, errors.New("compaction provider requires a delegate")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Provider{delegate: delegate, options: options}, nil
}

// Stream compacts a defensive request copy and delegates exactly once. It
// performs no model, tool, process, network, or interaction call of its own.
func (provider *Provider) Stream(ctx context.Context, request model.Request) (model.Stream, error) {
	if provider == nil || provider.delegate == nil {
		return nil, errors.New("compaction provider is unavailable")
	}
	if ctx == nil {
		return nil, errors.New("compaction provider requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	compacted, _, err := Compact(request, provider.options)
	if err != nil {
		return nil, err
	}
	return provider.delegate.Stream(ctx, compacted)
}

// SemanticIdentity binds portable snapshot compatibility to the exact local
// compaction semantics. The returned value fits Agent's public 256-byte
// compatibility-identity bound.
func SemanticIdentity(application string, options Options) (string, error) {
	if application == "" || application != strings.TrimSpace(application) {
		return "", errors.New("compaction application identity must be non-empty without surrounding whitespace")
	}
	if err := options.Validate(); err != nil {
		return "", err
	}
	configuration := fmt.Sprintf("%s\x00%d\x00%d\x00%d", ContractVersion, options.TriggerBytes, options.RetainRecentRounds, options.MaximumSummaryBytes)
	digest := sha256.Sum256([]byte(configuration))
	suffix := fmt.Sprintf("|compaction:%x", digest[:16])
	if len(application)+len(suffix) > maximumIdentityBytes {
		return "", fmt.Errorf("compaction semantic identity exceeds %d bytes", maximumIdentityBytes)
	}
	return application + suffix, nil
}

// Compact deterministically replaces an old contiguous sequence of complete
// assistant-tool rounds in one transient provider request. It never mutates
// request-owned values and never drops an incomplete call/result occurrence.
func Compact(request model.Request, options Options) (model.Request, Report, error) {
	if err := options.Validate(); err != nil {
		return model.Request{}, Report{}, err
	}
	messages := request.Messages()
	validated, err := model.NewRequest(request.OperationID(), request.Model(), messages, request.Tools())
	if err != nil {
		return model.Request{}, Report{}, fmt.Errorf("validate compaction input: %w", err)
	}
	inputBytes := messageBytes(messages)
	report := Report{InputBytes: inputBytes, OutputBytes: inputBytes}
	if inputBytes <= options.TriggerBytes {
		return validated, report, nil
	}
	rounds := completeRounds(messages)
	if len(rounds) <= options.RetainRecentRounds {
		return validated, report, nil
	}
	eligible := rounds[:len(rounds)-options.RetainRecentRounds]
	selected := contiguousPrefix(eligible)
	if len(selected) == 0 {
		return validated, report, nil
	}
	start, end := selected[0].start, selected[len(selected)-1].end
	summary, digest, err := summarize(messages[start:end], len(selected), options.MaximumSummaryBytes)
	if err != nil {
		return model.Request{}, Report{}, err
	}
	id, err := summaryID(messages, digest)
	if err != nil {
		return model.Request{}, Report{}, err
	}
	part, err := message.Text(summary)
	if err != nil {
		return model.Request{}, Report{}, err
	}
	summaryMessage, err := message.New(id, message.RoleSystem, part)
	if err != nil {
		return model.Request{}, Report{}, err
	}
	compactedMessages := make([]message.Message, 0, len(messages)-(end-start)+1)
	compactedMessages = append(compactedMessages, cloneMessages(messages[:start])...)
	compactedMessages = append(compactedMessages, summaryMessage)
	compactedMessages = append(compactedMessages, cloneMessages(messages[end:])...)
	compacted, err := model.NewRequest(request.OperationID(), request.Model(), compactedMessages, request.Tools())
	if err != nil {
		return model.Request{}, Report{}, fmt.Errorf("construct compacted request: %w", err)
	}
	outputBytes := messageBytes(compactedMessages)
	if outputBytes >= inputBytes {
		return validated, report, nil
	}
	report = Report{
		Compacted: true, Rounds: len(selected), Messages: end - start,
		InputBytes: inputBytes, OutputBytes: outputBytes, SourceDigest: digest,
	}
	return compacted, report, nil
}

type round struct{ start, end int }

func completeRounds(messages []message.Message) []round {
	var result []round
	for index := 0; index < len(messages); index++ {
		current := messages[index]
		if current.Role() != message.RoleAssistant {
			continue
		}
		pending := make(map[string]string)
		for _, part := range current.Parts() {
			if part.Kind() == message.PartToolCall {
				pending[part.CallID()] = part.Name()
			}
		}
		if len(pending) == 0 {
			continue
		}
		end := index + 1
		valid := true
		for end < len(messages) && len(pending) != 0 {
			candidate := messages[end]
			if candidate.Role() != message.RoleTool {
				valid = false
				break
			}
			for _, part := range candidate.Parts() {
				if part.Kind() != message.PartToolResult || pending[part.CallID()] != part.Name() {
					valid = false
					break
				}
				delete(pending, part.CallID())
			}
			if !valid {
				break
			}
			end++
		}
		if valid && len(pending) == 0 {
			result = append(result, round{start: index, end: end})
			index = end - 1
		}
	}
	return result
}

func contiguousPrefix(rounds []round) []round {
	if len(rounds) == 0 {
		return nil
	}
	count := 1
	for count < len(rounds) && rounds[count-1].end == rounds[count].start {
		count++
	}
	return rounds[:count]
}

func summarize(messages []message.Message, rounds, maximum int) (string, string, error) {
	digest := digestMessages(messages)
	header := fmt.Sprintf("[Spice deterministic compaction %s]\nCompacted %d complete tool rounds (%d messages).\nSource SHA-256: %s\nExtracted transcript:\n", ContractVersion, rounds, len(messages), digest)
	if len(header) > maximum {
		return "", "", errors.New("compaction summary bound cannot contain its deterministic header")
	}
	var output strings.Builder
	output.Grow(min(maximum, len(header)+messageBytes(messages)))
	output.WriteString(header)
	truncated := false
	for _, value := range messages {
		for _, part := range value.Parts() {
			line := summaryLine(value, part)
			if output.Len()+len(line) > maximum {
				marker := "[extract truncated; source digest covers all compacted messages]\n"
				available := maximum - output.Len() - len(marker)
				if available > 0 {
					output.WriteString(truncateUTF8(line, available))
				}
				truncated = true
				break
			}
			output.WriteString(line)
		}
		if truncated {
			break
		}
	}
	if truncated {
		marker := "[extract truncated; source digest covers all compacted messages]\n"
		output.WriteString(marker)
	}
	return output.String(), digest, nil
}

func summaryLine(value message.Message, part message.Part) string {
	prefix := string(value.Role()) + " " + strconv.Quote(string(value.ID())) + ": "
	switch part.Kind() {
	case message.PartText:
		text, _ := part.TextValue()
		return prefix + "text " + strconv.Quote(text) + "\n"
	case message.PartToolCall:
		return prefix + "tool_call " + strconv.Quote(part.Name()) + " " + strconv.Quote(part.CallID()) + " " + string(part.Data()) + "\n"
	case message.PartToolResult:
		return prefix + "tool_result " + strconv.Quote(part.Name()) + " " + strconv.Quote(part.CallID()) + " " + string(part.Data()) + "\n"
	case message.PartExtension:
		return prefix + "extension " + strconv.Quote(part.Namespace()) + " " + string(part.Data()) + "\n"
	default:
		return prefix + "unsupported\n"
	}
}

func digestMessages(messages []message.Message) string {
	hash := sha256.New()
	for _, value := range messages {
		writeField(hash, []byte(value.ID()))
		writeField(hash, []byte(value.Role()))
		for _, part := range value.Parts() {
			writeField(hash, []byte(part.Kind()))
			text, _ := part.TextValue()
			writeField(hash, []byte(text))
			writeField(hash, []byte(part.Name()))
			writeField(hash, []byte(part.CallID()))
			writeField(hash, []byte(part.Namespace()))
			writeField(hash, part.Data())
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

type writer interface{ Write([]byte) (int, error) }

func writeField(destination writer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write(value)
}

func summaryID(messages []message.Message, digest string) (message.ID, error) {
	seen := make(map[message.ID]struct{}, len(messages))
	for _, value := range messages {
		seen[value.ID()] = struct{}{}
	}
	base := "spice-compaction-" + digest[:32]
	for attempt := 0; attempt <= len(messages); attempt++ {
		candidate := base
		if attempt != 0 {
			candidate += "-" + strconv.Itoa(attempt)
		}
		id, err := message.NewID(candidate)
		if err != nil {
			return "", err
		}
		if _, collision := seen[id]; !collision {
			return id, nil
		}
	}
	return "", errors.New("compaction could not allocate a deterministic summary message ID")
}

func messageBytes(messages []message.Message) int {
	total := 0
	for _, value := range messages {
		total += value.SizeBytes()
	}
	return total
}

func cloneMessages(messages []message.Message) []message.Message {
	result := make([]message.Message, len(messages))
	for index, value := range messages {
		result[index] = value.Clone()
	}
	return result
}

func truncateUTF8(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

var _ model.Provider = (*Provider)(nil)
