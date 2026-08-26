// SPDX-License-Identifier: GPL-3.0-or-later
//
// libFuzzer-Ziel fuer den FIG-Walk und den FIG-0/15-Parser.
//
// Ein Bitparser ohne Referenzimplementierung ist genau der Fall, fuer den sich
// das lohnt. Bauen mit clang:
//
//   cmake -B build-fuzz -DFUZZING=ON -DCMAKE_C_COMPILER=clang \
//         -DCMAKE_CXX_COMPILER=clang++ -DCMAKE_BUILD_TYPE=RelWithDebInfo
//   cmake --build build-fuzz --target fuzz_fig0_15
//   ./build-fuzz/fuzz_fig0_15 -max_len=32

#include "controller.h"
#include "record.h"

#include "fib-processor.h"
#include "radio-controller.h"

#include <cstddef>
#include <cstdint>
#include <vector>

namespace {

class SinkController : public RadioControllerInterface {
public:
    void onSNR(float) override {}
    void onFrequencyCorrectorChange(int, int) override {}
    void onSyncChange(char) override {}
    void onSignalPresence(bool) override {}
    void onServiceDetected(uint32_t) override {}
    void onNewEnsemble(uint16_t) override {}
    void onSetEnsembleLabel(DabLabel&) override {}
    void onDateTimeUpdate(const dab_date_time_t&) override {}
    void onFIBDecodeSuccess(bool, const uint8_t*) override {}
    void onNewImpulseResponse(std::vector<float>&&) override {}
    void onConstellationPoints(std::vector<DSPCOMPLEX>&&) override {}
    void onNewNullSymbol(std::vector<DSPCOMPLEX>&&) override {}
    void onTIIMeasurement(tii_measurement_t&&) override {}
    void onMessage(message_level_t, const std::string&, const std::string&) override {}

    void onAsaAlert(const asa_alert_t& alert) override
    {
        // Denselben Weg gehen wie im Betrieb, damit auch die Serialisierung
        // unter Beschuss steht.
        bool reportable = false;
        asamon::Record record;
        record.kind = asamon::RecordKind::Asa;
        record.payload = asamon::asaPayloadFrom(alert, reportable);
        volatile size_t length = asamon::serialize(record).size();
        (void)length;
    }
};

}  // namespace

extern "C" int LLVMFuzzerTestOneInput(const uint8_t* data, size_t size)
{
    // Ein FIB sind 256 Bit; das Backend uebergibt sie als ein Byte je Bit.
    std::vector<uint8_t> bits(256, 0);
    for (size_t i = 0; i < size && i < 256; ++i) {
        bits[i] = static_cast<uint8_t>(data[i] & 1);
    }

    SinkController controller;
    FIBProcessor processor(controller);
    processor.processFIB(bits.data(), 0);
    return 0;
}
