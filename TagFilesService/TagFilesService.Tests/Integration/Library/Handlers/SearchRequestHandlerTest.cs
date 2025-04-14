using TagFilesService.Library;
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
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.Tags.Add(new("tag4"));
        DbContext.FilesMetadata.Add(new("file1", FileType.Unknown, null));
        DbContext.FilesMetadata.Add(new("file2", FileType.Unknown, null));
        DbContext.FilesMetadata.Add(new("file3", FileType.Unknown, null));
        await DbContext.SaveChangesAsync();

        MetadataService service = new(DbContext);
        await service.AssignTags(1u, [1u, 2u]);
        await service.AssignTags(2u, [1u, 3u]);
        await service.AssignTags(1u, [1u, 2u, 3u]);

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
        DbContext.FilesMetadata.Add(new("file1", FileType.Image, null));
        DbContext.FilesMetadata.Add(new("file2", FileType.Image, null));
        DbContext.FilesMetadata.Add(new("file3", FileType.Image, null));
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        PaginatedList<LibraryItemDto> result = await handler.Handle(new(string.Empty, 1, 20), CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file3", result.Items[0].Path);
        Assert.AreEqual("library/file2", result.Items[1].Path);
        Assert.AreEqual("library/file1", result.Items[2].Path);
    }
}