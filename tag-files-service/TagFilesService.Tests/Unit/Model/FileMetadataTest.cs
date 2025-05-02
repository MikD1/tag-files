using TagFilesService.Model;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class FileMetadataTest
{
    [DataTestMethod]
    [DataRow("image/jpeg", FileType.Image)]
    [DataRow("image/png", FileType.Image)]
    [DataRow("video/mp4", FileType.Video)]
    [DataRow("application/json", FileType.Unknown)]
    public void FileMetadata_Constructor_ShouldCorrectDeterminateFileType(string mediaType, FileType expectedFileType)
    {
        FileMetadata metadata = new FileMetadata("test-file.md", mediaType, null);

        Assert.AreEqual(expectedFileType, metadata.Type);
    }
}