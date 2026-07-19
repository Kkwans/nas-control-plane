package agentsocket

import (
	"encoding/base64"
	"errors"
	"math"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/protobuf/types/known/structpb"
)

const maxTerminalMessageBytes = 32 * 1024

func terminalMessageToStruct(message terminal.Message) (*structpb.Struct, error) {
	fields := map[string]any{"type": string(message.Type)}
	switch message.Type {
	case terminal.MessageStart:
		fields["target"] = string(message.Target)
		fields["rows"] = message.Rows
		fields["cols"] = message.Cols
	case terminal.MessageStarted, terminal.MessageClosed:
		if message.SessionID == "" {
			return nil, errors.New("terminal session id is required")
		}
		fields["sessionId"] = message.SessionID
	case terminal.MessageInput, terminal.MessageOutput:
		if len(message.Data) == 0 || len(message.Data) > maxTerminalMessageBytes {
			return nil, errors.New("terminal data size is invalid")
		}
		fields["data"] = base64.StdEncoding.EncodeToString(message.Data)
	case terminal.MessageResize:
		fields["rows"] = message.Rows
		fields["cols"] = message.Cols
	case terminal.MessageClose:
	default:
		return nil, errors.New("terminal message type is invalid")
	}
	return structpb.NewStruct(fields)
}

func terminalMessageFromStruct(input *structpb.Struct) (terminal.Message, error) {
	if input == nil {
		return terminal.Message{}, errors.New("terminal message is required")
	}
	fields := input.AsMap()
	messageType, ok := fields["type"].(string)
	if !ok || messageType == "" {
		return terminal.Message{}, errors.New("terminal message type is required")
	}
	message := terminal.Message{Type: terminal.MessageType(messageType)}
	if !containsOnlyTerminalFields(fields, terminalFieldsFor(message.Type)) {
		return terminal.Message{}, errors.New("terminal message contains unsupported fields")
	}

	switch message.Type {
	case terminal.MessageStart:
		target, ok := fields["target"].(string)
		if !ok || target == "" {
			return terminal.Message{}, errors.New("terminal target is required")
		}
		rows, err := terminalNumberField(fields, "rows")
		if err != nil {
			return terminal.Message{}, err
		}
		cols, err := terminalNumberField(fields, "cols")
		if err != nil {
			return terminal.Message{}, err
		}
		message.Target = terminal.Target(target)
		message.Rows = rows
		message.Cols = cols
	case terminal.MessageInput, terminal.MessageOutput:
		encoded, ok := fields["data"].(string)
		if !ok || encoded == "" {
			return terminal.Message{}, errors.New("terminal data is required")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(data) == 0 || len(data) > maxTerminalMessageBytes {
			return terminal.Message{}, errors.New("terminal data is invalid")
		}
		message.Data = data
	case terminal.MessageResize:
		rows, err := terminalNumberField(fields, "rows")
		if err != nil {
			return terminal.Message{}, err
		}
		cols, err := terminalNumberField(fields, "cols")
		if err != nil {
			return terminal.Message{}, err
		}
		message.Rows = rows
		message.Cols = cols
	case terminal.MessageStarted, terminal.MessageClosed:
		sessionID, ok := fields["sessionId"].(string)
		if !ok || sessionID == "" {
			return terminal.Message{}, errors.New("terminal session id is required")
		}
		message.SessionID = sessionID
	case terminal.MessageClose:
	default:
		return terminal.Message{}, errors.New("terminal message type is invalid")
	}
	return message, nil
}

func terminalFieldsFor(messageType terminal.MessageType) map[string]struct{} {
	fields := map[string]struct{}{"type": {}}
	switch messageType {
	case terminal.MessageStart:
		fields["target"] = struct{}{}
		fields["rows"] = struct{}{}
		fields["cols"] = struct{}{}
	case terminal.MessageInput, terminal.MessageOutput:
		fields["data"] = struct{}{}
	case terminal.MessageResize:
		fields["rows"] = struct{}{}
		fields["cols"] = struct{}{}
	case terminal.MessageStarted, terminal.MessageClosed:
		fields["sessionId"] = struct{}{}
	}
	return fields
}

func containsOnlyTerminalFields(fields map[string]any, allowed map[string]struct{}) bool {
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return false
		}
	}
	return true
}

func terminalNumberField(fields map[string]any, name string) (uint16, error) {
	value, ok := fields[name].(float64)
	if !ok || value < 0 || value > math.MaxUint16 || math.Trunc(value) != value {
		return 0, errors.New("terminal dimensions are invalid")
	}
	return uint16(value), nil
}
