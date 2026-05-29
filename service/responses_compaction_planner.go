package service

import (
	"errors"

	"github.com/QuantumNous/new-api/dto"
)

const DefaultResponsesCompactionRetainTurns = 8

type ResponsesCompactionPlan struct {
	OlderMessages    []dto.Message
	RetainedMessages []dto.Message
	NeedsSummary     bool
}

type responsesCompactionUnit struct {
	Start int
	End   int
}

func BuildResponsesCompactionPlan(messages []dto.Message, retainTurns int) (*ResponsesCompactionPlan, error) {
	if retainTurns <= 0 {
		return nil, errors.New("retain turns must be positive")
	}
	clonedMessages := append([]dto.Message(nil), messages...)
	plan := &ResponsesCompactionPlan{
		RetainedMessages: clonedMessages,
		NeedsSummary:     false,
	}
	if len(clonedMessages) == 0 {
		return plan, nil
	}

	units := buildResponsesCompactionUnits(clonedMessages)
	if len(units) <= retainTurns {
		return plan, nil
	}

	splitIndex := units[len(units)-retainTurns].Start
	splitIndex = adjustCompactionSplitForToolAdjacency(clonedMessages, splitIndex)
	if splitIndex <= 0 {
		return plan, nil
	}

	older := append([]dto.Message(nil), clonedMessages[:splitIndex]...)
	retained := append([]dto.Message(nil), clonedMessages[splitIndex:]...)
	if len(older) == 0 {
		return plan, nil
	}

	return &ResponsesCompactionPlan{
		OlderMessages:    older,
		RetainedMessages: retained,
		NeedsSummary:     true,
	}, nil
}

func buildResponsesCompactionUnits(messages []dto.Message) []responsesCompactionUnit {
	units := make([]responsesCompactionUnit, 0, len(messages))
	for idx := 0; idx < len(messages); {
		if isAssistantToolCallMessage(messages[idx]) {
			end := idx + 1
			for end < len(messages) && messages[end].Role == "tool" {
				end++
			}
			units = append(units, responsesCompactionUnit{Start: idx, End: end})
			idx = end
			continue
		}
		units = append(units, responsesCompactionUnit{Start: idx, End: idx + 1})
		idx++
	}
	return units
}

func adjustCompactionSplitForToolAdjacency(messages []dto.Message, splitIndex int) int {
	if splitIndex <= 0 || splitIndex >= len(messages) {
		return splitIndex
	}
	if messages[splitIndex].Role != "tool" {
		return splitIndex
	}

	for splitIndex > 0 && messages[splitIndex-1].Role == "tool" {
		splitIndex--
	}
	if splitIndex > 0 && isAssistantToolCallMessage(messages[splitIndex-1]) {
		splitIndex--
	}
	return splitIndex
}

func isAssistantToolCallMessage(msg dto.Message) bool {
	if msg.Role != "assistant" {
		return false
	}
	return len(msg.ParseToolCalls()) > 0
}
