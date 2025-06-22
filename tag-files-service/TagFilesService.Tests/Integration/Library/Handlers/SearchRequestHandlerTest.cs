using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class SearchRequestHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task Search_ShouldReturnLastItems_WhenRequestEmpty()
    {
        DbContext.LibraryItems.Add(new("file1", FileType.Image, null));
        DbContext.LibraryItems.Add(new("file2", FileType.Image, null));
        DbContext.LibraryItems.Add(new("file3", FileType.Image, null));
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, null, 1, 20);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file3", result.Items[0].Path);
        Assert.AreEqual("library/file2", result.Items[1].Path);
        Assert.AreEqual("library/file1", result.Items[2].Path);
    }

    [TestMethod]
    public async Task Search_ShouldReturnCorrectItems_WhenTagsSpecified()
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

        LibraryItem libraryItem1 = new("file1", FileType.Image, null);
        libraryItem1.Tags.AddRange(tags[0], tags[1]);
        LibraryItem libraryItem2 = new("file2", FileType.Image, null);
        libraryItem2.Tags.AddRange(tags[0], tags[2]);
        LibraryItem libraryItem3 = new("file3", FileType.Image, null);
        libraryItem3.Tags.AddRange(tags[0], tags[2], tags[3]);
        DbContext.LibraryItems.AddRange(libraryItem1, libraryItem2, libraryItem3);
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        const string tagQuery = "tag1 && (tag2 || tag3) && !tag4";
        SearchRequest request = new(tagQuery, null, 1, 20);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(2, result.Items.Count);
        Assert.AreEqual("library/file2", result.Items[0].Path);
        Assert.AreEqual("library/file1", result.Items[1].Path);
    }

    [DataTestMethod]
    [DataRow(FileType.Image, new[] { 1u, 3u, 4u })]
    [DataRow(FileType.Video, new[] { 2u, 6u })]
    public async Task Search_ShouldReturnCorrectItems_WhenItemTypeSpecified(FileType itemType, uint[] expectedIds)
    {
        DbContext.LibraryItems.Add(new("file1", FileType.Image, null)); // id: 1
        DbContext.LibraryItems.Add(new("file2", FileType.Video, null)); // id: 2
        DbContext.LibraryItems.Add(new("file3", FileType.Image, null)); // id: 3
        DbContext.LibraryItems.Add(new("file4", FileType.Image, null)); // id: 4
        DbContext.LibraryItems.Add(new("file5", FileType.Unknown, null)); // id: 5
        DbContext.LibraryItems.Add(new("file5", FileType.Video, null)); // id: 6
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, itemType, 1, 20);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(expectedIds.Length, result.Items.Count);
        foreach (LibraryItemDto item in result.Items)
        {
            Assert.IsTrue(expectedIds.Contains(item.Id), $"Item '{item.Id}' not expected");
        }
    }

    [TestMethod]
    public async Task Search_ShouldReturnCorrectItems_WhenTagsAndItemsTypeSpecified()
    {
        List<Tag> tags =
        [
            new("tag1"),
            new("tag2"),
            new("tag3")
        ];
        DbContext.Tags.AddRange(tags);
        await DbContext.SaveChangesAsync();

        LibraryItem libraryItem1 = new("file1", FileType.Image, null);
        libraryItem1.Tags.AddRange(tags[0], tags[1]);
        LibraryItem libraryItem2 = new("file2", FileType.Video, null);
        libraryItem2.Tags.AddRange(tags[0], tags[2]);
        LibraryItem libraryItem3 = new("file3", FileType.Image, null);
        libraryItem3.Tags.AddRange(tags[2]);
        LibraryItem libraryItem4 = new("file4", FileType.Image, null);
        libraryItem4.Tags.AddRange(tags[1]);
        DbContext.LibraryItems.AddRange(libraryItem1, libraryItem2, libraryItem3, libraryItem4);
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        const string tagQuery = "tag1 || tag3";
        SearchRequest request = new(tagQuery, FileType.Image, 1, 20);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(2, result.Items.Count);
        Assert.AreEqual("library/file3", result.Items[0].Path);
        Assert.AreEqual("library/file1", result.Items[1].Path);
    }

    [TestMethod]
    public async Task Search_ShouldReturnTagsSortedByName()
    {
        List<Tag> tags =
        [
            new("tagC"),
            new("tagA"),
            new("tagB")
        ];
        DbContext.Tags.AddRange(tags);
        await DbContext.SaveChangesAsync();

        LibraryItem libraryItem = new("file1", FileType.Image, null);
        libraryItem.Tags.AddRange(tags);
        DbContext.LibraryItems.Add(libraryItem);
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, null, 1, 20);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual("tagA", result.Items[0].Tags[0]);
        Assert.AreEqual("tagB", result.Items[0].Tags[1]);
        Assert.AreEqual("tagC", result.Items[0].Tags[2]);
    }
}