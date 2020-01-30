package qpdf

// #cgo CFLAGS: -I${SRCDIR}/../c-deps/qpdf/include
// #cgo LDFLAGS: -L${SRCDIR}/../c-deps/zlib -L${SRCDIR}/../c-deps/libjpeg-turbo -L${SRCDIR}/../c-deps/qpdf/libqpdf/build/.libs -lqpdf -lz -ljpeg -lstdc++
// #include <stdlib.h>
// #include <qpdf/qpdf-c.h>
import "C"
import "unsafe"

const (
	ObjectStreamDisable  = C.qpdf_o_disable
	ObjectStreamPreserve = C.qpdf_o_preserve
	ObjectStreamGenerate = C.qpdf_o_generate

	StreamDataUncompress = C.qpdf_s_uncompress
	StreamDataPreserve   = C.qpdf_s_preserve
	StreamDataCompress   = C.qpdf_s_compress
)

type qpdfError struct {
	msg string
}

func (e *qpdfError) Error() string {
	return e.msg
}

var alreadyClosedError = &qpdfError{"QPDF instance already closed"}

type QPDF struct {
	data   C.qpdf_data
	closed bool
}

func New() (*QPDF, error) {
	q := QPDF{
		data: C.qpdf_init(),
	}
	if err := q.getError(); err != nil {
		return nil, err
	}
	return &q, nil
}

func (q *QPDF) getError() error {
	if C.qpdf_has_error(q.data) != C.QPDF_TRUE {
		return nil
	}
	e := C.qpdf_get_error(q.data)
	// XXX are we responsible to free the error message char*, or QPDF?
	return &qpdfError{C.GoString(C.qpdf_get_error_full_text(q.data, e))}
}

func (q *QPDF) Close() error {
	if q.closed {
		return alreadyClosedError
	}
	C.qpdf_cleanup(&q.data)
	q.closed = true
	return nil
}

func (q *QPDF) ReadFile(filename string) error {
	if q.closed {
		return alreadyClosedError
	}
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))
	C.qpdf_read(q.data, cFilename, nil)
	if err := q.getError(); err != nil {
		return err
	}
	return nil
}

func (q *QPDF) SetQDFMode(v bool) {
	if q.closed {
		return
	}
	var qv C.QPDF_BOOL = C.QPDF_FALSE
	if v {
		qv = C.QPDF_TRUE
	}
	C.qpdf_set_qdf_mode(q.data, qv)
}

func (q *QPDF) SetCompressStreams(v bool) {
	if q.closed {
		return
	}
	var qv C.QPDF_BOOL = C.QPDF_FALSE
	if v {
		qv = C.QPDF_TRUE
	}
	C.qpdf_set_compress_streams(q.data, qv)
}

func (q *QPDF) SetSuppressWarnings(v bool) {
	if q.closed {
		return
	}
	var qv C.QPDF_BOOL = C.QPDF_FALSE
	if v {
		qv = C.QPDF_TRUE
	}
	C.qpdf_set_suppress_warnings(q.data, qv)
}

func (q *QPDF) SetObjectStreamMode(v int) {
	if q.closed {
		return
	}
	C.qpdf_set_object_stream_mode(q.data, C.enum_qpdf_object_stream_e(v))
}

func (q *QPDF) SetStreamDataMode(v int) {
	if q.closed {
		return
	}
	C.qpdf_set_stream_data_mode(q.data, C.enum_qpdf_stream_data_e(v))
}

func (q *QPDF) InitFileWrite(filename string) error {
	if q.closed {
		return alreadyClosedError
	}
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))
	C.qpdf_init_write(q.data, cFilename)
	if err := q.getError(); err != nil {
		return err
	}
	return nil
}

func (q *QPDF) Write() error {
	if q.closed {
		return alreadyClosedError
	}
	C.qpdf_write(q.data)
	if err := q.getError(); err != nil {
		return err
	}
	return nil
}
