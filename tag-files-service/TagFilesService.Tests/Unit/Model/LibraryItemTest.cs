using TagFilesService.Model;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class LibraryItemTest
{
    [DataTestMethod]
    [DataRow("test-file.txt", ".jpg", "test-file.jpg")]
    [DataRow("archive.tar.gz", ".zip", "archive.tar.zip")]
    [DataRow("no-extension", ".txt", "no-extension.txt")]
    public void ChangeFileExtension_ShouldReturnCorrectFileName(string fileName, string newExtension, string expected)
    {
        string result = LibraryItem.ChangeFileExtension(fileName, newExtension);

        Assert.AreEqual(expected, result);
    }

    [TestMethod]
    public void ToggleFavorite_ShouldToggleFavoriteStatus()
    {
        LibraryItem libraryItem = new("test-file.jpg", FileType.Image, null);
        Assert.IsFalse(libraryItem.IsFavorite);

        libraryItem.ToggleFavorite();
        Assert.IsTrue(libraryItem.IsFavorite);

        libraryItem.ToggleFavorite();
        Assert.IsFalse(libraryItem.IsFavorite);
    }
}