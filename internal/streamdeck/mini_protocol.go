package streamdeck

const (
	// MiniProductID is the USB product ID for the original Stream Deck Mini.
	// The 2022 Mini uses PID 0x0090 and is not enabled pending hardware PID
	// confirmation.
	MiniProductID uint16 = 0x0063

	// MiniKeyCount is the number of LCD keys on Stream Deck Mini.
	MiniKeyCount = 6
	// MiniKeyImageWidth is the required Mini key image width in pixels.
	MiniKeyImageWidth = 80
	// MiniKeyImageHeight is the required Mini key image height in pixels.
	MiniKeyImageHeight = 80

	miniInputReportSize   = 65
	miniOutputReportSize  = 1024
	miniFeatureReportSize = 17

	miniInputReportID         byte = 0x01
	miniOutputReportID        byte = 0x02
	miniCommandUpdateKeyImage byte = 0x01
	miniBrightnessReportID    byte = 0x05
	miniCommandSetBrightness  byte = 0x55
	miniBrightnessMagic0      byte = 0xaa
	miniBrightnessMagic1      byte = 0xd1
	miniBrightnessMagic2      byte = 0x01
	miniImageHeaderSize            = 16
	miniImageChunkSize             = miniOutputReportSize - miniImageHeaderSize
)

func meaningfulMiniInputBytes(report []byte) []byte {
	if len(report) > MiniKeyCount+1 {
		return report[:MiniKeyCount+1]
	}
	return report
}
