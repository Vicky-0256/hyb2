// In-process benchmark for the unmodified LinearCoFold Turner implementation.
// Build with the reference's usual flags and, for example:
//   -DLINEARCOFOLD_SOURCE='"/absolute/path/src/LinearCoFold.cpp"'
// Usage: linearcofold-benchmark BEAM REPETITIONS WARMUPS < one-pair.txt
// Input is one already reference-ordered pair of uppercase ACGU strands,
// separated by '&' without spaces. The harness never normalizes or reorders.
//
// Each steady_clock interval includes fresh parser construction, quiet parse,
// and parser destruction. Input preparation, result validation, ampersand
// insertion, and TSV output are outside the interval. All output is deferred
// until every repetition completes, so parse's fflush(stdout) has no earlier
// benchmark rows to flush. The reference source itself remains unchanged.

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
#error "Compile with -Dlv to benchmark physical Turner energies"
#endif

#define main linearcofold_reference_main
#include LINEARCOFOLD_SOURCE
#undef main

namespace {

int integer_argument(const char* text, const char* name, bool positive) {
    if (*text == '\0') {
        throw std::invalid_argument(std::string(name) + " must be an integer");
    }
    for (const char* cursor = text; *cursor != '\0'; ++cursor) {
        if (*cursor < '0' || *cursor > '9') {
            throw std::invalid_argument(std::string(name) + " must contain only digits");
        }
    }
    errno = 0;
    char* end = NULL;
    const long value = std::strtol(text, &end, 10);
    if (errno == ERANGE || *end != '\0' || value > INT_MAX ||
        value < (positive ? 1 : 0)) {
        throw std::invalid_argument(std::string(name) + " is out of range");
    }
    return static_cast<int>(value);
}

struct TimedResult {
    BeamCKYParser::DecoderResult result;
    long long elapsed_ns;
};

TimedResult fold_once(std::string& sequence, int cutpoint, int beam) {
    TimedResult timed;
    const auto start = std::chrono::steady_clock::now();
    {
        // Parser-owned containers are destroyed before the end timestamp.
        // Fresh instances also prevent retained special-loop cache entries.
        BeamCKYParser parser(beam, false);
        timed.result = parser.parse(sequence, cutpoint);
    }
    const auto end = std::chrono::steady_clock::now();
    timed.elapsed_ns = std::chrono::duration_cast<std::chrono::nanoseconds>(
                           end - start).count();
    return timed;
}

void validate_result(const BeamCKYParser::DecoderResult& result, size_t length) {
    if (result.structure.size() != length ||
        !std::isfinite(result.score / -100.0)) {
        throw std::runtime_error("reference returned an invalid structure or energy");
    }
    int depth = 0;
    for (char symbol : result.structure) {
        if (symbol == '(') {
            ++depth;
        } else if (symbol == ')') {
            if (--depth < 0) {
                throw std::runtime_error("reference returned unbalanced parentheses");
            }
        } else if (symbol != '.') {
            throw std::runtime_error("reference returned an unsupported structure symbol");
        }
    }
    if (depth != 0) {
        throw std::runtime_error("reference returned unbalanced parentheses");
    }
}

}  // namespace

int main(int argc, char** argv) {
    try {
        if (argc != 4) {
            throw std::invalid_argument(
                "usage: linearcofold-benchmark BEAM REPETITIONS WARMUPS");
        }
        const int beam = integer_argument(argv[1], "beam", false);
        const int repetitions = integer_argument(argv[2], "repetitions", true);
        const int warmups = integer_argument(argv[3], "warmups", false);

        std::string marked_sequence;
        if (!std::getline(std::cin, marked_sequence)) {
            throw std::invalid_argument("expected one canonical A&B sequence on stdin");
        }
        if (!marked_sequence.empty() && marked_sequence.back() == '\r') {
            marked_sequence.pop_back();
        }
        std::string extra_line;
        if (std::getline(std::cin, extra_line)) {
            throw std::invalid_argument("expected exactly one input line");
        }
        const size_t cut = marked_sequence.find('&');
        if (cut == std::string::npos || cut == 0 ||
            cut + 1 == marked_sequence.size() ||
            marked_sequence.find('&', cut + 1) != std::string::npos ||
            marked_sequence.size() - 1 > static_cast<size_t>(INT_MAX)) {
            throw std::invalid_argument("expected two nonempty strands separated by one &");
        }
        for (char base : marked_sequence) {
            if (base != '&' && base != 'A' && base != 'C' &&
                base != 'G' && base != 'U') {
                throw std::invalid_argument("sequence must contain only uppercase ACGU and &");
            }
        }
        std::string sequence = marked_sequence.substr(0, cut) +
                               marked_sequence.substr(cut + 1);
        const int cutpoint = static_cast<int>(cut);
        std::string expected_structure;
        value_type expected_score = 0;
        bool have_expected = false;
        std::vector<long long> elapsed_times;
        elapsed_times.reserve(static_cast<size_t>(repetitions));

        // Use separate loops to avoid overflowing warmups + repetitions.
        for (int phase = 0; phase < 2; ++phase) {
            const int count = phase == 0 ? warmups : repetitions;
            for (int iteration = 0; iteration < count; ++iteration) {
                const TimedResult timed = fold_once(sequence, cutpoint, beam);
                validate_result(timed.result, sequence.size());
                if (!have_expected) {
                    expected_structure = timed.result.structure;
                    expected_score = timed.result.score;
                    have_expected = true;
                } else if (timed.result.structure != expected_structure ||
                           timed.result.score != expected_score) {
                    throw std::runtime_error(
                        "reference output changed between warmup/measured repetitions");
                }
                if (phase == 1) {
                    elapsed_times.push_back(timed.elapsed_ns);
                }
            }
        }

        expected_structure.insert(cut, 1, '&');
        const double energy = expected_score / -100.0;
        std::cout << "elapsed_ns\tstructure\tenergy_kcal_mol\n";
        std::cout << std::fixed << std::setprecision(2);
        for (long long elapsed_ns : elapsed_times) {
            std::cout << elapsed_ns << '\t' << expected_structure << '\t'
                      << energy << '\n';
        }
        std::cout.flush();
        if (!std::cout) {
            throw std::runtime_error("failed to write benchmark TSV");
        }
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "linearcofold-benchmark: " << error.what() << '\n';
        return 1;
    }
}
