package streamdeck

import "fmt"

// Model identifies a supported Stream Deck hardware model.
type Model int

const (
	ModelUnknown Model = iota
	ModelPlus
	ModelMini
)

// String returns the model's human-readable name.
func (m Model) String() string {
	switch m {
	case ModelPlus:
		return "Stream Deck Plus"
	case ModelMini:
		return "Stream Deck Mini"
	default:
		return "Unknown Stream Deck"
	}
}

// ProductID returns the model's USB product ID.
func (m Model) ProductID() uint16 {
	switch m {
	case ModelPlus:
		return ProductID
	case ModelMini:
		return MiniProductID
	default:
		return 0
	}
}

// KeyCount returns the number of LCD keys on the model.
func (m Model) KeyCount() int {
	switch m {
	case ModelPlus:
		return KeyCount
	case ModelMini:
		return MiniKeyCount
	default:
		return 0
	}
}

// KeyImageSize returns the model's required key image dimensions.
func (m Model) KeyImageSize() (width, height int) {
	switch m {
	case ModelPlus:
		return KeyImageWidth, KeyImageHeight
	case ModelMini:
		return MiniKeyImageWidth, MiniKeyImageHeight
	default:
		return 0, 0
	}
}

// HasTouchStrip reports whether the model has the Plus touch strip.
func (m Model) HasTouchStrip() bool {
	return m == ModelPlus
}

func modelForProductID(productID uint16) (Model, bool) {
	switch productID {
	case ProductID:
		return ModelPlus, true
	case MiniProductID:
		return ModelMini, true
	default:
		return ModelUnknown, false
	}
}

func validateModel(model Model) error {
	if model.ProductID() == 0 {
		return fmt.Errorf("unsupported Stream Deck model %d", model)
	}
	return nil
}
