package curator

import (
	"encoding/json"
	"fmt"

	"imbue/internal/evidence"
)

type turnEvents struct {
	id     string
	events []evidence.Event
}

// Split keeps conversations separate and whole turns together whenever they fit.
// Boundary dialogue is explicitly context, never a second observation.
func Split(original Packet, limit int) ([]Packet, error) {
	if limit < 4096 {
		return nil, fmt.Errorf("packet limit too small")
	}
	p, err := excludePacketImages(original)
	if err != nil {
		return nil, err
	}
	order := []string{}
	conversations := map[string][]turnEvents{}
	indexes := map[string]map[string]int{}
	for _, ev := range p.Events {
		if indexes[ev.ConversationID] == nil {
			indexes[ev.ConversationID] = map[string]int{}
			order = append(order, ev.ConversationID)
		}
		key := ev.TurnID
		if key == "" {
			key = ev.ID
		}
		i, ok := indexes[ev.ConversationID][key]
		if !ok {
			i = len(conversations[ev.ConversationID])
			indexes[ev.ConversationID][key] = i
			conversations[ev.ConversationID] = append(conversations[ev.ConversationID], turnEvents{id: key})
		}
		groups := conversations[ev.ConversationID]
		groups[i].events = append(groups[i].events, ev)
		conversations[ev.ConversationID] = groups
	}
	out := []Packet{}
	for _, cid := range order {
		groups := conversations[cid]
		build := func(start, end int) Packet {
			part := p
			part.ConversationID = cid
			part.Events = nil
			part.ContextEvents = nil
			part.AllowedEvidenceIDs = nil
			part.Part = 999999
			part.Parts = 999999
			for _, g := range groups[start:end] {
				part.Events = append(part.Events, g.events...)
			}
			candidates := []evidence.Event{}
			if start == 0 {
				for _, ev := range p.ContextEvents {
					if ev.ConversationID == cid && isDialogue(ev) {
						candidates = append(candidates, ev)
					}
				}
			} else {
				candidates = append(candidates, boundaryDialogue(groups[start-1].events)...)
			}
			// Look ahead only as observed feedback, not information available to the action.
			if end < len(groups) {
				for _, ev := range groups[end].events {
					if ev.Kind == "user.message" {
						candidates = append(candidates, ev)
						break
					}
				}
			}
			selected := map[string]bool{}
			for _, ev := range part.Events {
				selected[ev.ID] = true
				part.AllowedEvidenceIDs = append(part.AllowedEvidenceIDs, ev.ID)
			}
			for _, ev := range candidates {
				if selected[ev.ID] {
					continue
				}
				selected[ev.ID] = true
				if len(ev.Payload) > limit/4 {
					ev.Kind = "context.fragment_reference"
					ev.Payload = json.RawMessage(`{"note":"補助文脈の本文は容量超過のため未提示。原本はEvidenceに保持。内容を推測しない。"}`)
				} else {
					part.AllowedEvidenceIDs = append(part.AllowedEvidenceIDs, ev.ID)
				}
				part.ContextEvents = append(part.ContextEvents, ev)
			}
			return part
		}
		for start := 0; start < len(groups); {
			bestEnd := start
			var best Packet
			for end := start + 1; end <= len(groups); end++ {
				trial := build(start, end)
				if packetWireSize(trial) > limit {
					break
				}
				bestEnd = end
				best = trial
			}
			if bestEnd > start {
				out = append(out, best)
				start = bestEnd
				continue
			}
			// A single oversized turn is the only reason to fragment raw text.
			fallback := build(start, start+1)
			parts, e := splitWithFragments(fallback, limit)
			if e != nil {
				return nil, e
			}
			for i := range parts {
				// The legacy fragmenter resets allowed IDs; restore visible boundary context.
				ids := map[string]bool{}
				for _, id := range parts[i].AllowedEvidenceIDs {
					ids[id] = true
				}
				for _, ev := range parts[i].ContextEvents {
					if ev.Kind != "context.fragment_reference" && !ids[ev.ID] {
						parts[i].AllowedEvidenceIDs = append(parts[i].AllowedEvidenceIDs, ev.ID)
						ids[ev.ID] = true
					}
				}
				if packetWireSize(parts[i]) > limit {
					return nil, fmt.Errorf("turn context exceeds packet budget")
				}
			}
			out = append(out, parts...)
			start++
		}
	}
	// Pack complete conversation chunks together when they fit; timeline IDs
	// retain their boundaries without paying one model call per small thread.
	packed := []Packet{}
	for _, part := range out {
		if len(packed) > 0 {
			candidate := mergePackets(packed[len(packed)-1], part)
			if packetWireSize(candidate) <= limit {
				packed[len(packed)-1] = candidate
				continue
			}
		}
		packed = append(packed, part)
	}
	out = packed
	if len(out) == 0 {
		return nil, fmt.Errorf("no evidence for job")
	}
	for i := range out {
		out[i].Part = i + 1
		out[i].Parts = len(out)
		if packetWireSize(out[i]) > limit {
			return nil, fmt.Errorf("model input exceeds packet budget")
		}
	}
	return out, nil
}

func isDialogue(e evidence.Event) bool { return e.Kind == "user.message" || e.Kind == "agent.message" }

// Include the previous request and final answer; avoid repeating every progress
// message. Unknown phases are preserved as possible answers, never invented.
func boundaryDialogue(events []evidence.Event) []evidence.Event {
	out := []evidence.Event{}
	var answer *evidence.Event
	for i := range events {
		e := events[i]
		if e.Kind == "user.message" {
			out = append(out, e)
		}
		if e.Kind == "agent.message" {
			var v struct {
				Phase string `json:"phase"`
			}
			json.Unmarshal(e.Payload, &v)
			if v.Phase != "commentary" {
				copy := e
				answer = &copy
			}
		}
	}
	if answer != nil {
		out = append(out, *answer)
	}
	return out
}

func packetWireSize(p Packet) int {
	aliased, _ := aliasEvidenceIDs(p)
	b, err := json.Marshal(modelInput(aliased))
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(b)
}

func mergePackets(a, b Packet) Packet {
	merged := a
	merged.Events = append(append([]evidence.Event(nil), a.Events...), b.Events...)
	merged.ContextEvents = nil
	seen := map[string]bool{}
	for _, e := range merged.Events {
		seen[e.ID] = true
	}
	for _, e := range append(append([]evidence.Event(nil), a.ContextEvents...), b.ContextEvents...) {
		if !seen[e.ID] {
			merged.ContextEvents = append(merged.ContextEvents, e)
			seen[e.ID] = true
		}
	}
	merged.AllowedEvidenceIDs = nil
	allowed := map[string]bool{}
	for _, id := range append(append([]string(nil), a.AllowedEvidenceIDs...), b.AllowedEvidenceIDs...) {
		if !allowed[id] {
			merged.AllowedEvidenceIDs = append(merged.AllowedEvidenceIDs, id)
			allowed[id] = true
		}
	}
	return merged
}
