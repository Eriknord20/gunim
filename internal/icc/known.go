package icc

// sRGBCurve is sRGB's transfer function as ICC's parametric type 3.
var sRGBCurve = Curve{Fn: 3, Params: [7]float64{2.4, 1 / 1.055, 0.055 / 1.055, 1 / 12.92, 0.04045}}

// SRGB returns the standard sRGB display profile, as matrix and curves.
func SRGB() *Profile {
	return &Profile{
		Description: "sRGB IEC61966-2.1", Version: 4, Class: "mntr", ColorSpace: "RGB ", PCS: "XYZ ",
		Matrix: sRGBMatrix, TRC: [3]Curve{sRGBCurve, sRGBCurve, sRGBCurve}, White: D50, HasMatrix: true,
	}
}

// DisplayP3 returns Display P3: the P3 primaries with a D65 white,
// adapted to D50, and sRGB's curve, as Apple's profile has them.
func DisplayP3() *Profile {
	return &Profile{
		Description: "Display P3", Version: 4, Class: "mntr", ColorSpace: "RGB ", PCS: "XYZ ",
		Matrix: [3][3]float64{
			{0.515121, 0.291977, 0.157104},
			{0.241196, 0.692245, 0.066574},
			{-0.001053, 0.041885, 0.784073},
		},
		TRC: [3]Curve{sRGBCurve, sRGBCurve, sRGBCurve}, White: D50, HasMatrix: true,
	}
}
