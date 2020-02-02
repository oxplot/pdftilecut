C_DEPS_DIR := $(abspath c-deps)
ZLIB_SRC_DIR    := $(C_DEPS_DIR)/zlib
LIBJPEG_SRC_DIR := $(C_DEPS_DIR)/libjpeg-turbo
QPDF_SRC_DIR    := $(C_DEPS_DIR)/qpdf

all: build

zlib: bin/.submodules-initialized
	cd $(ZLIB_SRC_DIR) && ./configure --static && make

libjpeg: bin/.submodules-initialized
	cd $(LIBJPEG_SRC_DIR) && cmake -G"Unix Makefiles" && make

qpdf: zlib libjpeg bin/.submodules-initialized
	cd $(QPDF_SRC_DIR)
	sh -x ./autogen.sh
	./configure
	LDFLAGS="-L$(ZLIB_SRC_DIR) -L$(LIBJPEG_SRC_DIR)" CFLAGS="-I$(ZLIB_SRC_DIR) -I$(LIBJPEG_SRC_DIR)" make

bin/pdftilecut: qpdf zlib libjpeg
	go build -o bin/pdftilecut -ldflags '-extldflags "-static"'

bin/.submodules-initialized:
ifneq ($$(git rev-parse --git-dir 2>/dev/null),)
	git submodule update --init --recursive
endif
	mkdir -p $(@D)
	touch $@

build: bin/pdftilecut
	
.PHONY: clean
clean:
	( cd $(ZLIB_SRC_DIR) && make clean )
	( cd $(LIBJPEG_SRC_DIR) && make clean )
	( cd $(QPDF_SRC_DIR) && make clean )
