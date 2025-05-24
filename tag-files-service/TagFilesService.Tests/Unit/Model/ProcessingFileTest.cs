using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class ProcessingFileTest
{
    [DataTestMethod]
    [DataRow("video-1.mp4", "video/mp4", FileType.Video, ".mp4")]
    [DataRow("video-2.avi", "video/x-msvideo", FileType.Video, ".mp4")]
    [DataRow("video-3.webm", "video/webm", FileType.Video, ".mp4")]
    public void VideoFileNameAndType_ShouldInitializeCorrect(string fileName, string contentType,
        FileType expectedFileType,
        string expectedLibraryFileExtensin)
    {
        ProcessingFile processingFile = new(fileName, contentType);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(expectedLibraryFileExtensin));
        Assert.AreEqual("video/mp4", processingFile.ContentType);
        Assert.AreEqual(expectedFileType, processingFile.FileType);
    }

    [DataTestMethod]
    [DataRow("photo-1.jpg", "image/jpeg", FileType.Image, ".jpg")]
    [DataRow("photo-2.png", "image/png", FileType.Image, ".png")]
    public void ImageFileNameAndType_ShouldInitializeCorrect(string fileName, string contentType,
        FileType expectedFileType,
        string expectedLibraryFileExtensin)
    {
        ProcessingFile processingFile = new(fileName, contentType);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(expectedLibraryFileExtensin));
        Assert.AreEqual(contentType, processingFile.ContentType);
        Assert.AreEqual(expectedFileType, processingFile.FileType);
    }
}