using Microsoft.Extensions.Logging;
using Moq;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class GetSimilarLibraryItemsHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task GetSimilarLibraryItemsRequest_ShouldReturnItemsWithSameTags_WhenItemsShareTags()
    {
        List<Tag> tags =
        [
            new("tag1"),
            new("tag2"),
            new("tag3"),
            new("tag4"),
            new("tag5")
        ];
        DbContext.Tags.AddRange(tags);
        await DbContext.SaveChangesAsync();

        LibraryItem item1 = new("file1", FileType.Image, null); // Source
        item1.Tags.AddRange(tags[0], tags[1], tags[4]);
        LibraryItem item2 = new("file2", FileType.Image, null); // Not similar
        item2.Tags.AddRange(tags[0], tags[4]);
        LibraryItem item3 = new("file3", FileType.Image, null); // Similar
        item3.Tags.AddRange(tags[0], tags[1], tags[4]);
        LibraryItem item4 = new("file4", FileType.Image, null); // Not similar
        item4.Tags.AddRange(tags[0], tags[1], tags[2], tags[4]);
        LibraryItem item5 = new("file5", FileType.Image, null); // Not similar
        item5.Tags.AddRange(tags[1], tags[4]);
        LibraryItem item6 = new("file6", FileType.Image, null); // Similar
        item6.Tags.AddRange(tags[4], tags[1], tags[0]);
        LibraryItem item7 = new("file7", FileType.Image, null); // Not similar (no tags)
        DbContext.LibraryItems.AddRange(item1, item2, item3, item4, item5, item6, item7);
        await DbContext.SaveChangesAsync();

        Mock<ILogger<GetSimilarLibraryItemsHandler>> loggerMock = new();
        GetSimilarLibraryItemsHandler handler = new(loggerMock.Object, DbContext);
        GetSimilarLibraryItemsRequest request = new(1u);
        List<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(2, result.Count);
        Assert.AreEqual("library/file6", result[0].Path);
        Assert.AreEqual("library/file3", result[1].Path);

        Assert.AreEqual(3, result[0].Tags.Count);
        Assert.AreEqual("tag1", result[0].Tags[0]);
        Assert.AreEqual("tag2", result[0].Tags[1]);
        Assert.AreEqual("tag5", result[0].Tags[2]);

        Assert.AreEqual(3, result[1].Tags.Count);
        Assert.AreEqual("tag1", result[1].Tags[0]);
        Assert.AreEqual("tag2", result[1].Tags[1]);
        Assert.AreEqual("tag5", result[1].Tags[2]);
    }

    [TestMethod]
    public async Task GetSimilarLibraryItemsRequest_ShouldReturnEmptyList_WhenSourceItemHasNoTags()
    {
        List<Tag> tags =
        [
            new("tag1"),
            new("tag2")
        ];
        DbContext.Tags.AddRange(tags);
        await DbContext.SaveChangesAsync();

        LibraryItem item1 = new("file1", FileType.Image, null); // Source (no tags)
        LibraryItem item2 = new("file2", FileType.Image, null); // Not similar
        item2.Tags.AddRange(tags[0]);
        DbContext.LibraryItems.AddRange(item1, item2);
        await DbContext.SaveChangesAsync();

        Mock<ILogger<GetSimilarLibraryItemsHandler>> loggerMock = new();
        GetSimilarLibraryItemsHandler handler = new(loggerMock.Object, DbContext);
        GetSimilarLibraryItemsRequest request = new(1u);
        List<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(0, result.Count);
    }
}