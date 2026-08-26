# SPDX-License-Identifier: GPL-3.0-or-later
#
# asamon-rx — Empfangsprozess des ASA-Monitors.
# Linkt statisch gegen das CMake-Ziel `welle` aus dem eingebundenen welle.io-Quellbaum;
# das gebaute Binary ist damit GPL-3.0-or-later (siehe TODO.md Abschnitt 4).

cmake_minimum_required(VERSION 3.16)
project(asamon-rx LANGUAGES C CXX VERSION 0.1.0)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

option(BUILD_TESTING "Einheitstests bauen" ON)
option(FUZZING       "libFuzzer-Ziel fuer den FIG-0/15-Parser bauen (clang)" OFF)

# --- Versionsangaben fuer den init-Record -----------------------------------
# Stehen vor add_subdirectory(), weil welle.io den eigenen Commit-Hash sonst
# aus unserem Verzeichnis holt (siehe unten).
find_package(Git QUIET)
function(git_describe_of dir hashOut describeOut)
    set(hash "unknown")
    set(described "unknown")
    if(GIT_FOUND)
        execute_process(COMMAND ${GIT_EXECUTABLE} -C ${dir} rev-parse --short=7 HEAD
                        OUTPUT_VARIABLE hash OUTPUT_STRIP_TRAILING_WHITESPACE
                        ERROR_QUIET RESULT_VARIABLE rc)
        if(NOT rc EQUAL 0 OR hash STREQUAL "")
            set(hash "unknown")
        endif()
        execute_process(COMMAND ${GIT_EXECUTABLE} -C ${dir} describe --tags --long
                        OUTPUT_VARIABLE described OUTPUT_STRIP_TRAILING_WHITESPACE
                        ERROR_QUIET RESULT_VARIABLE rc)
        if(NOT rc EQUAL 0 OR described STREQUAL "")
            set(described "unknown")
        endif()
    endif()
    set(${hashOut} ${hash} PARENT_SCOPE)
    set(${describeOut} ${described} PARENT_SCOPE)
endfunction()
git_describe_of(${CMAKE_CURRENT_SOURCE_DIR} RX_COMMIT RX_DESCRIBE)
git_describe_of(${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io WELLE_COMMIT WELLE_DESCRIBE)
configure_file(${CMAKE_CURRENT_SOURCE_DIR}/src/version.h.in
               ${CMAKE_CURRENT_BINARY_DIR}/version.h @ONLY)

# --- welle.io ---------------------------------------------------------------
# Zwei Stellen in welle.io gehen davon aus, das oberste Projekt zu sein, und
# greifen auf ${CMAKE_SOURCE_DIR} zu. Eingebunden ueber add_subdirectory zeigt
# das auf uns:
#   1. der Suchpfad fuer FindFFTW3f.cmake und die uebrigen Module,
#   2. die git-Abfrage fuer GITHASH/GITDESCRIBE.
# Beides wird hier von aussen richtiggestellt — ohne dafuer den Fork anfassen
# zu muessen.
list(INSERT CMAKE_MODULE_PATH 0 ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/cmake/Modules)
set(GIT_COMMIT_HASH ${WELLE_COMMIT}   CACHE STRING "" FORCE)
set(GIT_DESCRIBE    ${WELLE_DESCRIBE} CACHE STRING "" FORCE)

# welle.io bringt sein eigenes project(), seine Optionen und install()-Regeln mit.
# RTLSDR ist dort per Vorgabe OFF; ohne diesen Schalter liefert CInputFactory
# stillschweigend ein Null-Device (TODO.md Abschnitt 3, Falle vor allen anderen).
set(BUILD_WELLE_IO   OFF CACHE BOOL "" FORCE)
set(BUILD_WELLE_CLI  OFF CACHE BOOL "" FORCE)
set(LIBWELLE_STATIC  ON  CACHE BOOL "" FORCE)
set(RTLSDR           ON  CACHE BOOL "" FORCE)
set(FDK_AAC          OFF CACHE BOOL "" FORCE)   # Lizenz, siehe TODO.md Abschnitt 4
add_subdirectory(external/welle.io EXCLUDE_FROM_ALL)

# welle.io setzt keine target_include_directories, sondern ein globales
# include_directories(). Das propagiert nach unten, nicht zu uns herauf —
# die Pfade muessen im eigenen Ziel stehen. ${CMAKE_CURRENT_BINARY_DIR} von
# welle.io gehoert dazu, dort liegen generierte Header.
set(WELLE_INCLUDE_DIRS
    ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/src
    ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/src/backend
    ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/src/input
    ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/src/output
    ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/src/various
    ${CMAKE_CURRENT_SOURCE_DIR}/external/welle.io/src/libs/fec
    ${CMAKE_CURRENT_BINARY_DIR}/external/welle.io)

# Threads::Threads wird von welle.io ebenfalls gesucht, aber das importierte
# Ziel gilt nur in dessen Verzeichnis-Bereich.
find_package(Threads REQUIRED)

# --- die eigentliche Binary --------------------------------------------------
set(ASAMON_RX_LIB_SOURCES
    src/options.cpp
    src/record.cpp
    src/writer.cpp
    src/controller.cpp
    src/commands.cpp
    src/recorder.cpp
    src/notify.cpp)

add_library(asamon-rx-core STATIC ${ASAMON_RX_LIB_SOURCES})
target_include_directories(asamon-rx-core PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/src
    ${CMAKE_CURRENT_BINARY_DIR}
    ${WELLE_INCLUDE_DIRS})
target_link_libraries(asamon-rx-core PUBLIC welle Threads::Threads)
target_compile_options(asamon-rx-core PRIVATE -Wall -Wextra -Wswitch)

add_executable(asamon-rx src/main.cpp)
target_link_libraries(asamon-rx PRIVATE asamon-rx-core)
target_compile_options(asamon-rx PRIVATE -Wall -Wextra -Wswitch)

# --- Tests -------------------------------------------------------------------
if(BUILD_TESTING)
    enable_testing()
    add_executable(test_record tests/test_record.cpp)
    target_link_libraries(test_record PRIVATE asamon-rx-core)
    target_compile_options(test_record PRIVATE -Wall -Wextra)
    add_test(NAME record COMMAND test_record)

    # Speist handgebaute FIBs direkt in FIBProcessor::processFIB() ein — ohne
    # Funk, ohne Prozessgrenze. Seit rohe FIBs nicht mehr uebertragen werden,
    # ist das die einzige Stelle, an der sich der Parser gegen die Norm pruefen
    # laesst (TODO.md Abschnitt 12).
    add_executable(test_fig0_15 tests/test_fig0_15.cpp tests/fib_builder.cpp
                            tests/location_codes.cpp)
    target_link_libraries(test_fig0_15 PRIVATE asamon-rx-core)
    target_include_directories(test_fig0_15 PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/tests)
    target_compile_definitions(test_fig0_15 PRIVATE
        ASAMON_FIXTURE_DIR="${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures")
    target_compile_options(test_fig0_15 PRIVATE -Wall -Wextra)
    add_test(NAME fig0_15 COMMAND test_fig0_15)

    add_executable(test_writer tests/test_writer.cpp)
    target_link_libraries(test_writer PRIVATE asamon-rx-core)
    target_compile_options(test_writer PRIVATE -Wall -Wextra)
    add_test(NAME writer COMMAND test_writer)

    add_executable(test_recorder tests/test_recorder.cpp)
    target_link_libraries(test_recorder PRIVATE asamon-rx-core)
    target_compile_options(test_recorder PRIVATE -Wall -Wextra)
    add_test(NAME recorder COMMAND test_recorder)

    # Prueft das Bitlayout der Location Codes gegen die Byte-Laengen in
    # ETSI TS 104 090, Tabelle A.19 — eine zweite normative Quelle neben
    # Annex E.
    add_executable(test_location_codes tests/test_location_codes.cpp
                                       tests/location_codes.cpp
                                       tests/fib_builder.cpp)
    target_include_directories(test_location_codes PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/tests)
    target_compile_options(test_location_codes PRIVATE -Wall -Wextra)
    add_test(NAME location_codes COMMAND test_location_codes)

    add_executable(test_commands tests/test_commands.cpp)
    target_link_libraries(test_commands PRIVATE asamon-rx-core)
    target_compile_options(test_commands PRIVATE -Wall -Wextra)
    add_test(NAME commands COMMAND test_commands)
endif()

if(FUZZING)
    add_executable(fuzz_fig0_15 tests/fuzz_fig0_15.cpp)
    target_link_libraries(fuzz_fig0_15 PRIVATE asamon-rx-core)
    target_compile_options(fuzz_fig0_15 PRIVATE -fsanitize=fuzzer,address)
    target_link_options(fuzz_fig0_15 PRIVATE -fsanitize=fuzzer,address)
endif()
