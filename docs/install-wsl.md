# Installing Hyb2 on Windows (WSL)

On Windows, Hyb2 runs in the Windows Subsystem for Linux (WSL). The prerequisites are the same as on Linux and macOS:

1. Install WSL.
2. Install Miniconda in WSL.
3. Install Java on Windows. It does not need to be installed in WSL.

After running `bin/hyb2_install`, do these steps by hand:

1. Make sure Java is installed in `C:\Program Files\Java`.
2. Copy the `third_party/varna/` folder somewhere on Windows, such as `C:\VARNA`.
3. Change the contents of `bin/VARNA.dir` from the default (for example `/home/user/hyb2/third_party/varna/build/jar/VARNAcmd.jar`) to `C:\VARNA\build\jar\VARNAcmd.jar`.

The commands are then the same as on Linux.

Contact laujianyou@live.com if you run into problems.
