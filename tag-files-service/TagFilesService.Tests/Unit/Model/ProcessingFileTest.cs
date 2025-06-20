using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class ProcessingFileTest
{
    [DataTestMethod]
    [DataRow("video-1.mp4")]
    [DataRow("video-2.avi")]
    [DataRow("video-3.webm")]
    public void VideoFile_ShouldInitializeCorrect(string fileName)
    {
        ProcessingFile processingFile = new(fileName, FileType.Video);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(".mp4"));
        Assert.AreEqual(FileType.Video, processingFile.FileType);
    }

    [DataTestMethod]
    [DataRow("photo-1.jpg", ".jpg")]
    [DataRow("photo-2.png", ".png")]
    public void ImageFile_ShouldInitializeCorrect(string fileName, string expectedLibraryFileExtensin)
    {
        ProcessingFile processingFile = new(fileName, FileType.Image);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(expectedLibraryFileExtensin));
        Assert.AreEqual(FileType.Image, processingFile.FileType);
    }
}