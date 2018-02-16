package main

type MSPSerialConfig struct {
	Identifier              uint8
	FunctionMask            uint16
	MSPBaudRateIndex        uint8
	GPSBaudRateIndex        uint8
	TelemetryBaudRateIndex  uint8
	PeripheralBaudRateIndex uint8 // Actually blackboxBaudRateIndex in BF
}
