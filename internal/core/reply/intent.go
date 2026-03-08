package reply

import (
	"ai-review-agent/internal/shared"
	"strings"
)

var intentKeywords = map[shared.ReplyIntent][]string{
	shared.IntentReject: {
		"false positive", "intentional", "not an issue", "by design", "disagree",
		"won't fix", "incorrect", "wrong", "sai",
	},
	shared.IntentQuestion: {
		"why", "how", "what", "explain", "?",
	},
	shared.IntentDiscuss: {
		"what about", "how about", "alternatively", "instead",
	},
	shared.IntentAgree: {
		"fixed", "done", "agree", "good catch", "will fix", "ok", "thanks", "thank you",
	},
	shared.IntentAcknowledge: {
		"noted", "ack", "will address later",
	},
}

// ClassifyIntent determines the intent of a user's reply based on keyword matching.
func ClassifyIntent(text string) shared.ReplyIntent {
	lower := strings.ToLower(text)

	// Priority order
	for _, intent := range []shared.ReplyIntent{
		shared.IntentReject, shared.IntentQuestion, shared.IntentDiscuss,
		shared.IntentAgree, shared.IntentAcknowledge,
	} {
		for _, kw := range intentKeywords[intent] {
			if strings.Contains(lower, kw) {
				return intent
			}
		}
	}
	return shared.IntentAcknowledge
}

// IntentToSignal maps a reply intent to a feedback signal.
func IntentToSignal(intent shared.ReplyIntent) shared.FeedbackSignal {
	switch intent {
	case shared.IntentAgree, shared.IntentAcknowledge:
		return shared.FeedbackSignalAccepted
	case shared.IntentReject:
		return shared.FeedbackSignalRejected
	default:
		return shared.FeedbackSignalNeutral
	}
}
