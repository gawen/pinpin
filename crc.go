package pinpin

func Crc32(b []byte) (r uint32) {
	r = 0xff_ff_ff_ff
	for idx := range len(b) {
		r = r ^ uint32(b[idx])<<24
		for range 8 {
			bit := r&0x80_00_00_00 != 0
			r = r << 1
			if bit {
				r = r ^ uint32(0x04_c1_1d_b7)
			}
		}
	}
	return
}
