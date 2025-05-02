using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class SearchRequestHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task SearchRequest_ShouldReturnCorrectMetadata()
    {
        List<Tag> tags =
        [
            new("tag1"),
            new("tag2"),
            new("tag3"),
            new("tag4")
        ];
        DbContext.Tags.AddRange(tags);
        await DbContext.SaveChangesAsync();

        FileMetadata metadata1 = new("file1", "image/png", null);
        metadata1.Tags.AddRange(tags[0], tags[1]);
        FileMetadata metadata2 = new("file2", "image/png", null);
        metadata2.Tags.AddRange(tags[0], tags[2]);
        FileMetadata metadata3 = new("file3", "image/png", null);
        metadata3.Tags.AddRange(tags[0], tags[2], tags[3]);
        DbContext.FilesMetadata.AddRange(metadata1, metadata2, metadata3);
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        const string tagQuery = "tag1 && (tag2 || tag3) && !tag4";
        PaginatedList<LibraryItemDto> result = await handler.Handle(new(tagQuery, 1, 20), CancellationToken.None);

        Assert.AreEqual(2, result.Items.Count);
        Assert.AreEqual("library/file2", result.Items[0].Path);
        Assert.AreEqual("library/file1", result.Items[1].Path);
    }

    [TestMethod]
    public async Task SearchRequest_ShouldReturnLastMetadata_WhenEmptyQuery()
    {
        DbContext.FilesMetadata.Add(new("file1", "image/png", null));
        DbContext.FilesMetadata.Add(new("file2", "image/png", null));
        DbContext.FilesMetadata.Add(new("file3", "image/png", null));
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        PaginatedList<LibraryItemDto> result = await handler.Handle(new(string.Empty, 1, 20), CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file3", result.Items[0].Path);
        Assert.AreEqual("library/file2", result.Items[1].Path);
        Assert.AreEqual("library/file1", result.Items[2].Path);
    }
}