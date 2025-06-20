using TagFilesService.Model;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class FileMetadataTest
{
    [DataTestMethod]
    [DataRow("test-file.txt", ".jpg", "test-file.jpg")]
    [DataRow("archive.tar.gz", ".zip", "archive.tar.zip")]
    [DataRow("no-extension", ".txt", "no-extension.txt")]
    public void ChangeFileExtension_ShouldReturnCorrectFileName(string fileName, string newExtension, string expected)
    {
        string result = FileMetadata.ChangeFileExtension(fileName, newExtension);

        Assert.AreEqual(expected, result);
    }

    [TestMethod]
    public void ToggleFavorite_ShouldToggleFavoriteStatus()
    {
        FileMetadata metadata = new("test-file.jpg", FileType.Image, null);
        Assert.IsFalse(metadata.IsFavorite);

        metadata.ToggleFavorite();
        Assert.IsTrue(metadata.IsFavorite);

        metadata.ToggleFavorite();
        Assert.IsFalse(metadata.IsFavorite);
    }
}