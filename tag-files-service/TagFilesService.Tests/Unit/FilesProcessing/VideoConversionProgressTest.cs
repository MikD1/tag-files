using TagFilesService.FilesProcessing;

namespace TagFilesService.Tests.Unit.FilesProcessing;

[TestClass]
public class VideoConversionProgressTest
{
    [TestMethod]
    public void FinalProgress_IsCorrect()
    {
        string[] ffmpegOutputLines =
        [
            "      ffmpeg version 7.1.1 Copyright (c) 2000-2025 the FFmpeg developers",
            "        built with Apple clang version 16.0.0 (clang-1600.0.26.6)",
            "        configuration: --prefix=/opt/homebrew/Cellar/ffmpeg/7.1.1_1 ...",
            "      Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'http://localhost:5010/temporary/fixing a bug in production.mp4':",
            "        Metadata:",
            "          major_brand     : isom",
            "          minor_version   : 512",
            "          compatible_brands: isomiso2avc1mp41",
            "          encoder         : Lavf60.16.100",
            "        Duration: 00:00:35.57, start: 0.000000, bitrate: 1048 kb/s",
            "        Stream #0:0[0x1](und): Video: h264 (Main) ...",
            "        Stream #0:1[0x2](und): Audio: aac (LC) ...",
            "      Stream mapping:",
            "        Stream #0:0 -> #0:0 (h264 (native) -> h264 (libx264))",
            "        Stream #0:1 -> #0:1 (aac (native) -> aac (native))",
            "      frame=  226 fps=0.0 q=29.0 size=      68KiB time=00:00:07.47 bitrate=  74.7kbits/s speed=14.8x",
            "      frame=  433 fps=429 q=29.0 size=    1161KiB time=00:00:14.38 bitrate= 661.4kbits/s speed=14.2x",
            "      frame=  615 fps=406 q=29.0 size=    2238KiB time=00:00:20.45 bitrate= 896.2kbits/s speed=13.5x",
            "      frame=  783 fps=388 q=29.0 size=    3586KiB time=00:00:26.05 bitrate=1127.3kbits/s speed=12.9x",
            "      frame=  975 fps=387 q=29.0 size=    3586KiB time=00:00:32.46 bitrate= 904.9kbits/s speed=12.9x",
            "      frame= 1063 fps=409 q=-1.0 Lsize=    5072KiB time=00:00:35.40 bitrate=1173.7kbits/s speed=13.6x"
        ];

        VideoConversionProgress progress = new();
        foreach (string line in ffmpegOutputLines)
        {
            progress.AddOutputLine(line);
        }

        Assert.AreEqual(TimeSpan.FromSeconds(35.57), progress.Total);
        Assert.AreEqual(TimeSpan.FromSeconds(35.399), progress.Current);
        Assert.AreEqual(99, progress.Percent);
    }

    [TestMethod]
    public void TrackProgressThroughoutConversion_IsCorrect()
    {
        string[] ffmpegOutputLines =
        [
            "        Duration: 00:00:35.57, start: 0.000000, bitrate: 1048 kb/s",
            "      frame=  226 fps=0.0 q=29.0 size=      68KiB time=00:00:07.47 bitrate=  74.7kbits/s speed=14.8x",
            "      frame=  433 fps=429 q=29.0 size=    1161KiB time=00:00:14.38 bitrate= 661.4kbits/s speed=14.2x",
            "      frame=  615 fps=406 q=29.0 size=    2238KiB time=00:00:20.45 bitrate= 896.2kbits/s speed=13.5x",
            "      frame=  783 fps=388 q=29.0 size=    3586KiB time=00:00:26.05 bitrate=1127.3kbits/s speed=12.9x",
            "      frame=  975 fps=387 q=29.0 size=    3586KiB time=00:00:32.46 bitrate= 904.9kbits/s speed=12.9x",
            "      frame= 1063 fps=409 q=-1.0 Lsize=    5072KiB time=00:00:35.40 bitrate=1173.7kbits/s speed=13.6x"
        ];

        (TimeSpan? Time, int Percent)[] expected =
        [
            (null, 0),
            (TimeSpan.FromSeconds(7.469), 20),
            (TimeSpan.FromSeconds(14.38), 40),
            (TimeSpan.FromSeconds(20.449), 57),
            (TimeSpan.FromSeconds(26.05), 73),
            (TimeSpan.FromSeconds(32.46), 91),
            (TimeSpan.FromSeconds(35.399), 99)
        ];

        VideoConversionProgress progress = new();
        for (int i = 0; i < ffmpegOutputLines.Length; ++i)
        {
            progress.AddOutputLine(ffmpegOutputLines[i]);

            Assert.AreEqual(expected[i].Time, progress.Current);
            Assert.AreEqual(expected[i].Percent, progress.Percent);
        }
    }
}