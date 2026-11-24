# Hyb2
***Easily Generate RNA structures of short- and long-range intra/intermolecular interactions, and homodimers with an Interactive Graphical User Interface.***

A ***streamlined*** program for analyzing RNA proximity ligation experiments from mapped files in the fastq/SAM format to:
1. Generate a list of chimeric interactions with their coordinates, sequence, and folding energy,
2. Plot contact density map of selected genes and viewpoint graphs,
3. Generate intra-/intermolecular RNA structure of any selected regions.

**Additionally, creates an interactive GUI, and plots differences and similarities between experiments.**

<p align="center">
  <img src="https://github.com/user-attachments/assets/fde2431b-f942-4a3e-90c5-f5e11f222d85" height="500" width="700">
</p>

## Table of Contents
- [Introduction](#introduction)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Entry Points](#entry-points)
- [Getting Started](#getting-started)
- [Running the Program](#running-the-program)
- [How To Read Outputs](#how-to-read-outputs)
- [License](#license)

## Introduction
RNA adopts ensemble of structures essential for life such as splicing, gene expression, and virus replication.
It adopts complex secondary structures supported by short-, long-range base-pairing interactions and tertiary structures supported by long-range interactions, pseudoknots, base triples, and RBPs.
RNA proximity ligation techniques such as CLASH, PARIS, and COMRADES directly detect RNA-RNA interactions and produces a list of chimeric interactions.
However, it is difficult to identify long-range base-pairings as it is often misidentified by minimum folding energy algorithms, and the interpretation of proximity ligation data is challenging as there is a lack of integrated analytical tools.

Hyb2 is a bioinformatics pipeline for the analysis of RNA proximity ligation experiments in high resolution.

It **generates RNA structures with experimental support** for short- and long-range intramolecular interactions, intermolecular interactions, and RNA homodimers.

It **supports commonly used data formats (SAM)** and integrates with a variety of mapping and analysis tools.

The Hyb2 pipeline is **streamlined** to receive input of fastq/SAM files and generates a hyb file with information on RNA-RNA interactions, and various visualizations of data: contact density map, viewpoint graph, and RNA structure as **outputs in a single command on the Linux command line**.

Creates a **GUI to easily select and visualize RNA-RNA interactions** from contact density maps, with styling options using VARNA. GUI allows selection of specific interactions directly from the contact density maps, and **generating the corresponding colour-coded RNA secondary structure** in a VARNA GUI pop-up.

## Prerequisites
Linux/ Mac Operating System with Miniconda Installed

1. **Miniconda**, if not installed, read based on your operating system (OS):

> https://docs.conda.io/projects/conda/en/latest/user-guide/install/linux.html
>
> https://docs.conda.io/projects/conda/en/latest/user-guide/install/macos.html

2. **Java** or **Java Development Kit (JDK)**:

Download the appropriate version based on your OS, locate the file, then install by double-clicking:
> https://www.oracle.com/java/technologies/downloads

#
Hyb2 can be used on **Windows** with Windows Subsystem for Linux (**WSL**) installed; see the [WSL installation guide](docs/install-wsl.md).

## Installation
Hyb2 can be downloaded and installed with:

```bash
git clone https://github.com/Vicky-0256/hyb2.git

# Install Hyb2 on Linux:
cd hyb2/
bin/hyb2_install

# On MacOS, install using:
cd hyb2/
bin/hyb2_install_macOS

# On MacOS-ARM, use:
cd hyb2/
bin/hyb2_install_macOS-arm
```

## Entry Points

The Hyb2 command-line pipeline is `hyb2pipe` in `pipeline/`. It turns FASTQ(.gz), FASTA or SAM input into `.hyb` files in pure Python (3.9+), with no Perl, awk or sed, so it also runs on Windows. The desktop app runs the same code for every sample. Run it from the repository root:

```bash
PYTHONPATH=pipeline python3 -m hyb2pipe -i data/testData.sam -d data/Zika_18S.fasta -o out/test -e 1
```

It writes `out/test.hyb`, `out/test_homodimers.hyb` and `out/test_hybrids.hyb_stats.txt`. `-e 1` adds the dG column and needs the ViennaRNA Python package or `RNAduplex` on the PATH; FASTQ and FASTA input also need Bowtie2. See [`pipeline/README.md`](pipeline/README.md) for every option, memory use and resuming.

`bin/` holds the original shell, Perl, awk and R scripts. `hyb2pipe` replaces their first step (`sam_filt`, `sam2hyb` and the `bowtie2_*` wrappers). The plotting and folding commands below still come from `bin/` and read the `.hyb` files that `hyb2pipe` writes.

| Step | Command | Location |
|---|---|---|
| FASTQ/FASTA/SAM to `.hyb` | `python3 -m hyb2pipe` (legacy: `hyb2 -i reads.sam ...`) | `pipeline/hyb2pipe/` (legacy: `bin/`) |
| Contact density maps, viewpoint graphs | `hyb2 -i test.hyb -a GENE` | `bin/` (R) |
| Structure folding and VARNA drawings | `hyb2_fold`, `hyb2 ... -x -y -l` | `bin/` |
| Interactive GUI | `hyb2_app` | `bin/` (R Shiny) |
| Differential coverage and similarity | `hyb2_compare` | `bin/` (R, DESeq2) |
| CPLfold and CPLcofold | `bin/cplfold` | `third_party/cplfold/` |
| IRIS | `bin/iris` | `pipeline/hyb2analysis/iris.py` |

`hyb2pipe` and `bin/hyb2` give byte-identical `.hyb` files for `data/testData.sam` without energies (`pipeline/tests/test_pipeline.py`). They differ on purpose in two places:

- **dG column (`-e 1`):** `hyb2pipe` folds the two arms with ViennaRNA RNAduplex, which forms intermolecular pairs only, as the original Hyb pipeline did. `bin/hyb2` uses RNAcofold, where each arm may also pair with itself. `--energy-engine rnacofold` gives the legacy values.
- **Order and ties:** `hyb2pipe` runs Bowtie2 with `--reorder` and resolves tied gene rankings by first-seen gene, so repeated runs give the same output.

Older `bin/hyb2` versions also wrote a TAB inside dG whenever |dG| < 10, which shifted every later column, and ignored `-h`. Both are fixed in `bin/` and were never present in `hyb2pipe`.

### CPLfold on the command line, web and desktop

CPLfold is part of Hyb2. Its Python package `cplfold` is in `third_party/cplfold`
(`import cplfold; cplfold.fold(sequence)`, or `pip install third_party/cplfold`),
and `bin/cplfold` provides it as a local CLI. Its core predictor requires
NumPy and Numba but does not require a native HotKnots executable. See
[`docs/cplfold-python.md`](docs/cplfold-python.md) for installation, verification,
runtime limits, browser boundaries, and third-party licensing notes.

Change shared CPLfold/CPLcofold algorithms in `third_party/cplfold/` and their
shared Python adapter in `web/cplfold/`. Do not copy edits into generated web
assets or the staged desktop runtime. On a push changing either path on
`CPLcofold`, `integration/unified-release` or `main`, CI builds the website and
desktop packages from the same commit and checks their source against Git.
On the website the predictor runs in a same-origin Pyodide worker, accepts
an optional HYB-derived block bonus matrix, and visualises/ranks nested and
pseudoknotted candidates. The browser path uses a measured session-specific
recommendation above a 75 nt baseline, with a 500 nt hard safety ceiling
without Numba; use the structure page's local-input download and run
`bin/cplfold --sequence-file cplfold-input.fasta --bonus-matrix-file cplfold-bonus-matrix.tsv`
for longer guided sequences. The download includes the prepared sequence and,
when HYB-guided CPLfold is selected, the same sparse bonus matrix used in the
browser. For CPLfold's licence terms, see [License](#license).

### IRIS from the source checkout

With Python 3.11, NumPy and the ViennaRNA Python bindings installed (`python -m pip install numpy ViennaRNA==2.7.2` in an active virtual environment), run:

```sh
bin/iris --reference ref.fa --gene toyRNA --start 31 --end 170 --hyb reads.hyb -o ensemble.json
```

The source CLI and Desktop use the same Python IRIS predictor. The CLI runs without the `web/` or `desktop/` trees. See the [IRIS CLI guide](docs/iris-cli.md) for input, output and the exact files to include in a standalone source archive.

## Getting Started
Activate the environment created during installation:
```bash
# Linux:
conda activate hyb2

# MacOS:
conda activate hyb2_macOS

# MacOS-ARM:
conda activate hyb2_macOS-arm
```
Then make the `.hyb` files with `hyb2pipe`, and plot and fold them with `hyb2`:
```bash
# Hyb2 is the directory Hyb2 was installed in.
# Step 1: SAM (or FASTQ/FASTA) to test.hyb, with folding energies
PYTHONPATH=Hyb2/pipeline python3 -m hyb2pipe -i Hyb2/data/testData.sam -d Hyb2/data/Zika_18S.fasta -o test -e 1

# Steps 2 and 3: contact density maps, viewpoint graph and the structure of nucleotides 1001-1500
hyb2 -i test.hyb -d Hyb2/data/Zika_18S.fasta -a Zika_virusRNA -x 1001 -l 500
```
To run the program, the first thing to have is the sequence alignment map (SAM) file or a fastq file. Here, we've provided only the SAM file.

If the fastq files from RNA proximity ligation experiments are not mapped to reference sequences, `hyb2pipe` maps them with bowtie2 when given the FASTQ file as `-i`.

The second file needed will contain the reference fasta sequences.

**VARNA** is used to visualize the RNA structures.

The legacy one-command run, `hyb2 -i Hyb2/data/testData.sam -d Hyb2/data/Zika_18S.fasta -o test -a Zika_virusRNA -x 1001 -l 500`, still works; see [Entry Points](#entry-points) for how its `.hyb` differs.

### Reference Fasta Files
The following ID format is provided to be more informative:

> \>Gene stable ID version, Transcript stable ID version, Gene name, Gene type
>
> \>ENSG00000007372.25_ENST00000638963.1_PAX6_mRNA

Or see:
> https://github.com/Vicky-0256/hyb2/blob/main/data/Zika_18S_formatted.fasta

## Running the Program
Hyb2 environment needs to be activated for essential softwares.

```bash
# Linux:
conda activate hyb2

# MacOS:
conda activate hyb2_macOS

# MacOS-ARM:
conda activate hyb2_macOS-arm
```
**Hyb2**

A run has 3 parts. `hyb2pipe` converts the input to Hyb format; `hyb2 -i <output>.hyb` plots the contact density maps and folds the RNA structures.

### Converting Input to Hyb Format
```bash
PYTHONPATH=Hyb2/pipeline python3 -m hyb2pipe -i <input> -d <reference.fasta> -o <output_prefix> [options]
```
-i input_file (fastq, fastq.gz, fasta or sam)

-d reference_sequences.fasta used for mapping

-o output_prefix

-v blast_threshold (default value: 0.1)

-m max_overlap (default value: 4)

-h max_hits_per_sequence (default value: 10)

-e folding_energy_prediction (default=0 (off), 1 to add the dG column)

--energy-engine dG engine with `-e 1` (default: rnaduplex; rnacofold gives the legacy hyb2 values)

-p Bowtie2 threads and worker processes (default: all CPUs)

--keep-sam also write `<output_prefix>.sam` (plain) or `.sam.gz` (gz) (default: none)

--memory-mb, --tmp-dir, --no-resume: memory budget, work directory and resuming; see [`pipeline/README.md`](pipeline/README.md)

The legacy `hyb2` takes the same `-i -d -o -v -m -h -e` options and runs this step itself when `-i` is not a `.hyb` file.

### Plotting Contact Density Map
These options and the folding options below go to `hyb2 -i <output_prefix>.hyb -d <reference.fasta>`.

-a gene_ID_of_interest

-b 2nd_gene_ID_of_interest

-q upperlimit_for_heatmap_chimeric_count (default value: 0.95 (%) )

### RNA Structure Folding
-x start_coord_of_Arm1 (cannot be longer than nucleotide length of specified gene)

-y start_coord_of_Arm2 (cannot be longer than nucleotide length of specified gene)

-l lengths_of_fragments

-j location of VARNAcmd.jar

-r select preferred RNA folding algorithm (default=1 (ViennaRNA), 0 for UNAFold (software not allowed to be distributed here), 2 for mfold 3.6)

mfold mode (`-r 2`) folds the structures with mfold 3.6 (`conda install -c bioconda mfold`), which applies the chimera constraints as forced base pairs; `bin/mfold_check` tests this before folding. mfold has no duplex folding, so in this mode the chimeras' basepair scores use ViennaRNA's RNAcofold, as in `-r 1`; so does the `.hyb` dG column when the legacy `hyb2` builds the `.hyb` itself. A `.hyb` from `hyb2pipe` already holds its dG (`--energy-engine`), which `-r` does not change. `bin/mfold_fold` runs each fold in a temporary folder with links to mfold's energy tables (the bioconda build finds them only in the current folder); `MFOLD` names another mfold script.

UNAFold mode needs the full UNAFold `hybrid-ss-min`, which applies the chimera constraints as forced base pairs. The `hybrid-ss-min` of OligoArrayAux (the conda `oligoarrayaux` package, installed for `hybrid-min`) silently ignores forced pairs, so `-r 0` stops with an error when it is the one found (`bin/unafold_check` tests this before folding). Put the full UNAFold's `hybrid-ss-min` first on `PATH`, or set `HYBRID_SS_MIN` to it, or use ViennaRNA (`-r 1`).

##
The examples below use the `test.hyb` made once from the test data:
```bash
PYTHONPATH=Hyb2/pipeline python3 -m hyb2pipe -i Hyb2/data/testData.sam -d Hyb2/data/Zika_18S.fasta -o test -e 1
```
### Analysis of short-range intramolecular interactions:
RNA Structure Folding from 1001-1500nt positions of Zika virus (ZIKV).
```bash
hyb2 -i test.hyb -d Hyb2/data/Zika_18S.fasta -a Zika_virusRNA -x 1001 -l 500
```
### Analysis of long-range intramolecular interactions:
RNA Structure Folding of 1001-1500nt positions with 5001-5500nt positions of ZIKV.
```bash
hyb2 -i test.hyb -d Hyb2/data/Zika_18S.fasta -a Zika_virusRNA -x 1001 -y 5001 -l 500
```
### Analysis of intermolecular interactions:
RNA Structure Folding of 7501-8000nt positions of ZIKV with 501-550nt positions of 18S rRNA.
```bash
hyb2 -i test.hyb -d Hyb2/data/Zika_18S.fasta -a Zika_virusRNA -b 18S_rRNA -x 7501 -y 501 -l 500
```
### Analysis of homodimer interactions:
RNA Structure Folding of 3501-3700nt positions of ZIKV with 3501-3700nt positions of a second strand of ZIKV.
```bash
hyb2 -i test.hyb -d Hyb2/data/Zika_18S.fasta -a Zika_virusRNA -b Zika_virusRNA -x 3501 -y 3501 -l 200
```

##
### Contact Density Maps and RNA Folding
To plot only the contact density maps of a `.hyb` file, without a viewpoint graph or structure:
```bash
hyb2 -i test.hyb -a 18S_rRNA

# Or
hyb2_coverage -i test.hyb -a 18S_rRNA
```
To also zoom-in on certain regions on the Contact Density Map:
```bash
hyb2 -i test.hyb -a 18S_rRNA -x 201 -y 201 -l 1200

# Or -w -x -y -z corresponds to x1 x2 y1 and y2
hyb2_coverage -i test.hyb -a 18S_rRNA -w 201 -x 1400 -y 201 -z 1400
```
To plot RNA structures directly using hyb file as input:
```bash
hyb2_fold -i test.hyb -d Hyb2/data/Zika_18S.fasta -a Zika_virusRNA -x 1 -y 1001 -l 200
```

##
### Randomized Parallel RNA Structure Folding
To perform randomized parellel RNA structure folding, which folds the RNA 1,000 times, and subsequently scoring each structure, a computer cluster that runs **qsub** is required.

Taking the analysis of intermolecular interactions as an example:
```bash
qsub comradesFold2 -c test_Zika_virusRNA-7501-8000_18S_rRNA-501-1000.1-1100_folding_constraints.txt -i Zika_virusRNA-7501-8000_18S_rRNA-501-1000_1-1100.fasta -s 1

comradesScore -i test_Zika_virusRNA-7501-8000_18S_rRNA-501-1000.basepair_scores.txt -f Zika_virusRNA-7501-8000_18S_rRNA-501-1000_1-1100.fasta
```
##
### Graphical User Interface
To create an interactive GUI pop-up window:
```bash
hyb2_app -i test.hyb -a Zika_virusRNA -d Hyb2/data/Zika_18S.hyb.fasta
```
##
### Differential Coverage Map and Similarity Heatmap
To compare between 2 different proximity ligation experiments, the program incorporates DESeq2 to identify the differential chimeras, and produces a differential coverage map.

To find the conservations between 2 experiments, we look for overlapping interactions.

A minimum of 2 replicates needed for each experiment to be used as input.

The input files for **hyb2_compare** comes from outputs of the main hyb2 pipeline, a **tab-delimited table needs to be made manually** in the format shown here:
> https://github.com/Vicky-0256/hyb2/blob/main/data/hyb2_compare_input.table

**MacOS-ARM cannot use hyb2_compare. Several packages in DESeq2 not supported in ARM architecture.**

For example, to identify the differences and similarities between control and experimental conditions:
```bash
hyb2_compare -i input.table -a Zika_virusRNA -d Hyb2/data/Zika_18S.hyb.fasta
```

## How To Read Outputs
### Hyb files
The first output is a **hyb** file, that contains sequence identifiers, read sequences, 1-based mapping coordinates, and annotation information for each chimera.

There's 17 columns per read:

Column 1: Unique Sequence Identifier

Column 2: Read Sequence

Column 3: Predicted binding energy in kcal/mol.

Columns 4–9: Mapping information for first fragment of read: name of matched transcript, coordinates in read, coordinates in transcript, mapping score.

Columns 10–15: Mapping information for second fragment of read.

Column 16: Overlap Score

Column 17: **Type of Chimera** (See chim_types for a visualization of the types of chimera)
> https://github.com/Vicky-0256/hyb2/blob/main/bin/chim_types
##
### Contact Density Maps
<p align="middle">
  <img src="https://user-images.githubusercontent.com/110675091/185672361-a9c49db9-9c93-4dff-aa3e-0545a70d36e8.png" height="330" width="330" />
  <img src="https://user-images.githubusercontent.com/110675091/185672386-7175cf64-733e-485e-9639-365df5ce8240.png" height="330" width="330" />
  <img src="https://user-images.githubusercontent.com/110675091/185671422-c84c289c-4b14-4505-847e-1bb869d99e89.png" height="330" width="330" />
</p>

The axis are the genome lengths with each spot representing chimeras.

Chimeras ligated in 5'-3' and 3'-5' orientations are plotted above and below the diagonal respectively.

Spots close to the diagonal are short-ranged interactions that can be easily folded into structures, while spots further away are long-ranged interactions.

The contrast of the spots corresponds to the chimeric counts, with darker spots representing a higher count.

Although the contrast is capped at an upper quantile limit (default 95%).

If 2 genes were input in the command line, the  nuclotide positions of the first gene will be plotted as the x-axis, and the other on y-axis.
##
### Viewpoint Graphs
<p align="center">
  <img src="https://user-images.githubusercontent.com/110675091/184885181-90b10327-67e6-44ed-87f6-81b896b8defc.png" height="200" width="350">
</p>

The x-axis represents the nucleotide positions and y-axis represents the frequency of chimeric interactions.

The graph shows the abundance of interactions and its positions along the RNA.


### RNA Structures
<p align="center">
  <img src="https://user-images.githubusercontent.com/110675091/184883583-d92dcdff-803a-4720-bbeb-d6085be9d30f.png" height="150" width="900">
</p>

To understand the secondary structures, read:
> https://varna.lri.fr/

The structures are colour coded based on log2 of supporting reads, with red being the most supported, blue the least, and blank for none.

(VARNA instructions)
##
### Hyb2 GUI
<p align="middle">
  <img src="https://github.com/Vicky-0256/hyb2/assets/110675091/b9105f5d-8d47-4ca3-9adb-e51e62012fd9" height="350" width="500" />
  <img src="https://github.com/Vicky-0256/hyb2/assets/110675091/c0dc4f6d-db8a-492b-85fb-272656882b66" height="300" width="450" />
</p>

The GUI shows 3 contact density maps. Highlighting the first contact density map (left) will zoom into the region (plotted in the middle). Highlighting an interaction on the second plot will generate a zoomed-in version of the interaction in the third contact density map (right).

The tables show the coordinates of each interaction, with chimeric counts shown. Uncapped counts refer to the actual chimeric counts, and count refer to counts capped at 95% percentile.

There's options to select what types of RNA-RNA interaction to generate, the start X (and Y) coordinates, and length of RNA to fold. X (and Y) coordinates are automatically inputted based on the highlighted interaction, but can also be entered manually.

Clicking the "Fold RNA" button creates a VARNA GUI pop-up with the colour-coded RNA structure generated.
##
### Differential Coverage Maps & Similarity Heatmap
<p align="center">
  <img src="https://github.com/Vicky-0256/hyb2/assets/110675091/a7b32e9d-4a4e-46c5-a141-d4df142bc9a1" height="300" width="600">
</p>

Read similarly to **Contact Density Maps**.

However, differential coverage maps are plotted in two colours, with red being interactions enriched in one condition and blue for the other.

Also, instead of the contrast reflecting chimeric counts, here, it represents significance (p-value) for significanyly differential interactions.

Similarity heatmaps plot the conserved interactions between datasets.

## License
Hyb2 is free to use for academic and commercial work. No fees are charged for any of its parts.

Hyb2's own code (`bin/`, `pipeline/`, `web/`, `desktop/`, and CPLfold and CPLcofold in `third_party/cplfold/`) is licensed under the GNU General Public License, version 3 or (at your option) any later version (GPL-3.0-or-later). The full text is in [`LICENSE`](LICENSE).

Hyb2 contains and distributes third-party components, each under its own licence: VARNA, IRIS, the LinearFold, LinearCoFold, HotKnots and ViennaRNA parts of CPLfold, D3 and fornac in this repository, and ViennaRNA, Pyodide, NumPy, Numba and Bowtie2 in the website and desktop builds. [`THIRD_PARTY_LICENSES.md`](THIRD_PARTY_LICENSES.md) lists each one, where it is distributed, and its licence.

The CPLfold combination is still being confirmed with the LinearFold authors. CPLfold's GPL part and the LinearFold-derived parser have different terms, and the combination does not yet have one confirmed licence. [`THIRD_PARTY_NOTICES.md`](third_party/cplfold/THIRD_PARTY_NOTICES.md) records the full terms and the original authors. If you publish results from CPLfold, please also cite LinearFold, LinearCoFold, ViennaRNA and HotKnots. If you publish IRIS results, please cite IRIS.
