// Split timings for the unchanged LinearCoFold Turner parser.
// Build with -Dlv and -DLINEARCOFOLD_SOURCE='"/absolute/path/LinearCoFold.cpp"'.
// Usage: linearcofold-profile BEAM REPETITIONS WARMUPS < one-pair.txt
// Input: two already ordered uppercase ACGU strands separated by '&'.
// Result validation and all output occur after the corresponding timers.

#include <array>
#include <cerrno>
#include <chrono>
#include <climits>
#include <cmath>
#include <cstdlib>
#include <iomanip>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

#ifndef LINEARCOFOLD_SOURCE
#error "Define LINEARCOFOLD_SOURCE as the quoted path to LinearCoFold.cpp"
#endif
#ifndef lv
#error "Compile with -Dlv for physical Turner energies"
#endif
#define main linearcofold_reference_main
#include LINEARCOFOLD_SOURCE
#undef main

namespace {
using Clock = std::chrono::steady_clock;

int argument(const char* text, bool positive) {
    if (!*text) throw std::invalid_argument("arguments must be unsigned integers");
    for (const char* p = text; *p; ++p) {
        if (*p < '0' || *p > '9')
            throw std::invalid_argument("arguments must be unsigned integers");
    }
    errno = 0;
    const long value = std::strtol(text, NULL, 10);
    if (errno == ERANGE || value > INT_MAX || value < (positive ? 1 : 0))
        throw std::invalid_argument("argument out of range");
    return static_cast<int>(value);
}

long long nanoseconds(Clock::time_point start, Clock::time_point end) {
    return std::chrono::duration_cast<std::chrono::nanoseconds>(end - start).count();
}

void validate(const BeamCKYParser::DecoderResult& result, size_t size) {
    const std::string& structure = result.structure;
    if (!std::isfinite(result.score / -100.0))
        throw std::runtime_error("nonfinite result energy");
    if (structure.size() != size) throw std::runtime_error("invalid result length");
    int depth = 0;
    for (char symbol : structure) {
        if (symbol == '(') ++depth;
        else if (symbol == ')') {
            if (--depth < 0) throw std::runtime_error("unbalanced result");
        } else if (symbol != '.') throw std::runtime_error("invalid result symbol");
    }
    if (depth) throw std::runtime_error("unbalanced result");
}
}  // namespace

int main(int argc, char** argv) {
    try {
        if (argc != 4)
            throw std::invalid_argument("usage: linearcofold-profile BEAM REPETITIONS WARMUPS");
        const int beam = argument(argv[1], false);
        const int repetitions = argument(argv[2], true);
        const int warmups = argument(argv[3], false);
        std::string sequence, extra;
        if (!std::getline(std::cin, sequence) || std::getline(std::cin, extra))
            throw std::invalid_argument("expected exactly one canonical A&B line");
        if (!sequence.empty() && sequence.back() == '\r') sequence.pop_back();
        const size_t cut = sequence.find('&');
        if (cut == std::string::npos || cut == 0 || cut + 1 == sequence.size() ||
            sequence.find('&', cut + 1) != std::string::npos ||
            sequence.size() - 1 > static_cast<size_t>(INT_MAX))
            throw std::invalid_argument("expected two nonempty strands separated by one &");
        for (char base : sequence) {
            if (base != 'A' && base != 'C' && base != 'G' && base != 'U' && base != '&')
                throw std::invalid_argument("sequence must contain only uppercase ACGU and &");
        }
        sequence.erase(cut, 1);
        std::vector<std::array<long long, 3>> samples;
        samples.reserve(static_cast<size_t>(repetitions));
        std::string expected_structure;
        value_type expected_score = 0;
        bool have_expected = false;
        for (int phase = 0; phase < 2; ++phase) {
            const int count = phase == 0 ? warmups : repetitions;
            for (int iteration = 0; iteration < count; ++iteration) {
                BeamCKYParser::DecoderResult result;
                Clock::time_point constructed, parsed;
                const auto start = Clock::now();
                {
                    BeamCKYParser parser(beam, false);
                    constructed = Clock::now();
                    result = parser.parse(sequence, static_cast<int>(cut));
                    parsed = Clock::now();
                }
                const auto destroyed = Clock::now();
                validate(result, sequence.size());
                if (!have_expected) {
                    expected_structure = result.structure;
                    expected_score = result.score;
                    have_expected = true;
                } else if (result.structure != expected_structure || result.score != expected_score) {
                    throw std::runtime_error("reference output changed between repetitions");
                }
                if (phase == 1) samples.push_back({{
                    nanoseconds(start, constructed), nanoseconds(constructed, parsed),
                    nanoseconds(parsed, destroyed)}});
            }
        }
        expected_structure.insert(cut, 1, '&');
        // Deferred output keeps the reference's fflush(stdout) empty while timed.
        std::cout << "constructor_ns\tparse_ns\tdestruction_ns\tstructure\tenergy_kcal_mol\n"
                  << std::fixed << std::setprecision(2);
        for (const auto& sample : samples)
            std::cout << sample[0] << '\t' << sample[1] << '\t' << sample[2] << '\t'
                      << expected_structure << '\t' << expected_score / -100.0 << '\n';
        std::cout.flush();
        if (!std::cout) throw std::runtime_error("failed to write profile TSV");
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "linearcofold-profile: " << error.what() << '\n';
        return 1;
    }
}
