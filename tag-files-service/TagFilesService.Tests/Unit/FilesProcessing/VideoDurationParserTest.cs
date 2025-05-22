using TagFilesService.FilesProcessing;

namespace TagFilesService.Tests.Unit.FilesProcessing;

[TestClass]
public class VideoDurationParserTest
{
    [DataTestMethod]
    [DataRow("00:00:35.57", 0, 0, 35, 570)]
    [DataRow("01:12:59.10", 1, 12, 59, 100)]
    [DataRow("00:00:00.42", 0, 0, 0, 420)]
    [DataRow("00:00:01.00", 0, 0, 1, 0)]
    public void Parse_ValidOutput_ReturnsExpectedTimeSpan(string time, int hours, int minutes, int seconds,
        int milliseconds)
    {
        const string template =
            "ffmpeg version 7.1.1 Copyright (c) 2000-2025 the FFmpeg developers\n  built with Apple clang version 16.0.0 (clang-1600.0.26.6)\n  configuration: --prefix=/opt/homebrew/Cellar/ffmpeg/7.1.1_1 --enable-shared --enable-pthreads --enable-version3 --cc=clang --host-cflags= --host-ldflags='-Wl,-ld_classic' --enable-ffplay --enable-gnutls --enable-gpl --enable-libaom --enable-libaribb24 --enable-libbluray --enable-libdav1d --enable-libharfbuzz --enable-libjxl --enable-libmp3lame --enable-libopus --enable-librav1e --enable-librist --enable-librubberband --enable-libsnappy --enable-libsrt --enable-libssh --enable-libsvtav1 --enable-libtesseract --enable-libtheora --enable-libvidstab --enable-libvmaf --enable-libvorbis --enable-libvpx --enable-libwebp --enable-libx264 --enable-libx265 --enable-libxml2 --enable-libxvid --enable-lzma --enable-libfontconfig --enable-libfreetype --enable-frei0r --enable-libass --enable-libopencore-amrnb --enable-libopencore-amrwb --enable-libopenjpeg --enable-libspeex --enable-libsoxr --enable-libzmq --enable-libzimg --disable-libjack --disable-indev=jack --enable-videotoolbox --enable-audiotoolbox --enable-neon\n  libavutil      59. 39.100 / 59. 39.100\n  libavcodec     61. 19.101 / 61. 19.101\n  libavformat    61.  7.100 / 61.  7.100\n  libavdevice    61.  3.100 / 61.  3.100\n  libavfilter    10.  4.100 / 10.  4.100\n  libswscale      8.  3.100 /  8.  3.100\n  libswresample   5.  3.100 /  5.  3.100\n  libpostproc    58.  3.100 / 58.  3.100\nInput #0, mov,mp4,m4a,3gp,3g2,mj2, from 'fixing a bug in production.mp4':\n  Metadata:\n    major_brand     : isom\n    minor_version   : 512\n    compatible_brands: isomiso2avc1mp41\n    encoder         : Lavf60.16.100\n  Duration: {{time}}, start: 0.000000, bitrate: 1048 kb/s\n  Stream #0:0[0x1](und): Video: h264 (Main) (avc1 / 0x31637661), yuv420p(tv, bt709, progressive), 964x720 [SAR 1:1 DAR 241:180], 915 kb/s, 29.97 fps, 29.97 tbr, 30k tbn (default)\n      Metadata:\n        handler_name    : ISO Media file produced by Google Inc.\n        vendor_id       : [0][0][0][0]\n  Stream #0:1[0x2](und): Audio: aac (LC) (mp4a / 0x6134706D), 44100 Hz, stereo, fltp, 127 kb/s (default)\n      Metadata:\n        handler_name    : ISO Media file produced by Google Inc.\n        vendor_id       : [0][0][0][0]";
        string output = template.Replace("{{time}}", time);

        bool success = VideoDurationParser.TryParse(output, out TimeSpan result);

        Assert.IsTrue(success);
        Assert.AreEqual(new(0, hours, minutes, seconds, milliseconds), result);
    }

    [TestMethod]
    public void Parse_InvalidOutput_ReturnsFalse()
    {
        const string output = "Invalid output from ffmpeg";

        bool success = VideoDurationParser.TryParse(output, out TimeSpan result);

        Assert.IsFalse(success);
        Assert.AreEqual(TimeSpan.Zero, result);
    }

    [TestMethod]
    public void Parse_InvalidDurationFormat_ReturnsFalse()
    {
        const string template =
            "ffmpeg version 7.1.1 Copyright (c) 2000-2025 the FFmpeg developers\n  built with Apple clang version 16.0.0 (clang-1600.0.26.6)\n  configuration: --prefix=/opt/homebrew/Cellar/ffmpeg/7.1.1_1 --enable-shared --enable-pthreads --enable-version3 --cc=clang --host-cflags= --host-ldflags='-Wl,-ld_classic' --enable-ffplay --enable-gnutls --enable-gpl --enable-libaom --enable-libaribb24 --enable-libbluray --enable-libdav1d --enable-libharfbuzz --enable-libjxl --enable-libmp3lame --enable-libopus --enable-librav1e --enable-librist --enable-librubberband --enable-libsnappy --enable-libsrt --enable-libssh --enable-libsvtav1 --enable-libtesseract --enable-libtheora --enable-libvidstab --enable-libvmaf --enable-libvorbis --enable-libvpx --enable-libwebp --enable-libx264 --enable-libx265 --enable-libxml2 --enable-libxvid --enable-lzma --enable-libfontconfig --enable-libfreetype --enable-frei0r --enable-libass --enable-libopencore-amrnb --enable-libopencore-amrwb --enable-libopenjpeg --enable-libspeex --enable-libsoxr --enable-libzmq --enable-libzimg --disable-libjack --disable-indev=jack --enable-videotoolbox --enable-audiotoolbox --enable-neon\n  libavutil      59. 39.100 / 59. 39.100\n  libavcodec     61. 19.101 / 61. 19.101\n  libavformat    61.  7.100 / 61.  7.100\n  libavdevice    61.  3.100 / 61.  3.100\n  libavfilter    10.  4.100 / 10.  4.100\n  libswscale      8.  3.100 /  8.  3.100\n  libswresample   5.  3.100 /  5.  3.100\n  libpostproc    58.  3.100 / 58.  3.100\nInput #0, mov,mp4,m4a,3gp,3g2,mj2, from 'fixing a bug in production.mp4':\n  Metadata:\n    major_brand     : isom\n    minor_version   : 512\n    compatible_brands: isomiso2avc1mp41\n    encoder         : Lavf60.16.100\n  Duration: {{time}}, start: 0.000000, bitrate: 1048 kb/s\n  Stream #0:0[0x1](und): Video: h264 (Main) (avc1 / 0x31637661), yuv420p(tv, bt709, progressive), 964x720 [SAR 1:1 DAR 241:180], 915 kb/s, 29.97 fps, 29.97 tbr, 30k tbn (default)\n      Metadata:\n        handler_name    : ISO Media file produced by Google Inc.\n        vendor_id       : [0][0][0][0]\n  Stream #0:1[0x2](und): Audio: aac (LC) (mp4a / 0x6134706D), 44100 Hz, stereo, fltp, 127 kb/s (default)\n      Metadata:\n        handler_name    : ISO Media file produced by Google Inc.\n        vendor_id       : [0][0][0][0]";
        string output = template.Replace("{{time}}", "01:99:35.57");

        bool success = VideoDurationParser.TryParse(output, out TimeSpan result);

        Assert.IsFalse(success);
        Assert.AreEqual(TimeSpan.Zero, result);
    }
}