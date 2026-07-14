package mcpstdio

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/Dirard/mcp-file-tools/internal/config"
)

type semanticIDDigest func(SemanticIDKey) [32]byte

type usedIDRegistryConfig struct {
	maxRequests   uint32
	tableSlots    int
	arenaMaxBytes int
}

type usedIDSlot struct {
	digest [32]byte
	offset uint32
	length uint16
	state  uint8
	_      uint8
}

const (
	usedIDSlotEmpty uint8 = iota
	usedIDSlotOccupied
)

type usedIDRegistry struct {
	slots         []usedIDSlot
	arena         []byte
	maxRequests   uint32
	arenaMaxBytes int
	count         uint32
	digest        semanticIDDigest
}

type idRegistration uint8

const (
	idRegistrationNew idRegistration = iota + 1
	idRegistrationDuplicate
	idRegistrationExhausted
)

type requestIDAdmissionKind uint8

const (
	requestIDIgnored requestIDAdmissionKind = iota + 1
	requestIDAccepted
	requestIDDuplicate
	requestIDExhausted
)

type requestIDAdmission struct {
	kind            requestIDAdmissionKind
	output          string
	closeConnection bool
}

func newUsedIDRegistry() *usedIDRegistry {
	return newUsedIDRegistryWithConfig(usedIDRegistryConfig{
		maxRequests:   uint32(config.SessionMaxRequests),
		tableSlots:    int(config.UsedIDTableSlots),
		arenaMaxBytes: int(config.UsedIDArenaMaxBytes),
	}, defaultSemanticIDDigest)
}

func newUsedIDRegistryWithConfig(settings usedIDRegistryConfig, digest semanticIDDigest) *usedIDRegistry {
	return &usedIDRegistry{
		slots:         make([]usedIDSlot, settings.tableSlots),
		arena:         make([]byte, 0, settings.arenaMaxBytes),
		maxRequests:   settings.maxRequests,
		arenaMaxBytes: settings.arenaMaxBytes,
		digest:        digest,
	}
}

func defaultSemanticIDDigest(key SemanticIDKey) [32]byte {
	return sha256.Sum256([]byte(key.encoded))
}

func (registry *usedIDRegistry) register(key SemanticIDKey) idRegistration {
	digest := registry.digest(key)
	index := int(binary.LittleEndian.Uint64(digest[:8]) % uint64(len(registry.slots)))
	for range len(registry.slots) {
		slot := &registry.slots[index]
		if slot.state == usedIDSlotEmpty {
			if registry.count >= registry.maxRequests || len(key.encoded) > registry.arenaMaxBytes-len(registry.arena) {
				return idRegistrationExhausted
			}
			offset := len(registry.arena)
			registry.arena = append(registry.arena, key.encoded...)
			*slot = usedIDSlot{
				digest: digest,
				offset: uint32(offset),
				length: uint16(len(key.encoded)),
				state:  usedIDSlotOccupied,
			}
			registry.count++
			return idRegistrationNew
		}
		if slot.digest == digest && registry.slotKeyEquals(*slot, key.encoded) {
			return idRegistrationDuplicate
		}
		index++
		if index == len(registry.slots) {
			index = 0
		}
	}
	return idRegistrationExhausted
}

func (registry *usedIDRegistry) slotKeyEquals(slot usedIDSlot, key string) bool {
	if int(slot.length) != len(key) {
		return false
	}
	start := int(slot.offset)
	for index := range len(key) {
		if registry.arena[start+index] != key[index] {
			return false
		}
	}
	return true
}

func (registry *usedIDRegistry) admit(message inboundMessage) requestIDAdmission {
	if message.kind != inboundRequest {
		return requestIDAdmission{kind: requestIDIgnored}
	}
	registration := registry.register(message.requestID.SemanticKey())
	if registration == idRegistrationNew {
		return requestIDAdmission{kind: requestIDAccepted}
	}
	if registration == idRegistrationDuplicate {
		return requestIDAdmission{
			kind:   requestIDDuplicate,
			output: invalidRequestForID(message.requestID),
		}
	}
	return requestIDAdmission{
		kind:            requestIDExhausted,
		output:          sessionRequestLimitForID(message.requestID),
		closeConnection: true,
	}
}

func invalidRequestForID(id RequestID) string {
	return string(encodeProtocolError(id, protocolInvalidRequest))
}

func sessionRequestLimitForID(id RequestID) string {
	return string(encodeProtocolError(id, protocolSessionRequestLimit))
}
