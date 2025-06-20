using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class GetTagsStatisticsHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task GetTagsStatisticsRequest_ShouldReturnCorrectStatistics()
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

        List<FileMetadata> metadata =
        [
            new("file1", FileType.Image, null),
            new("file2", FileType.Image, null),
            new("file3", FileType.Image, null),
            new("file4", FileType.Image, null),
            new("file5", FileType.Image, null),
            new("file6", FileType.Image, null),
            new("file7", FileType.Image, null),
        ];

        metadata[0].Tags.AddRange(tags[1], tags[4]);
        metadata[1].Tags.AddRange(tags[1]);
        metadata[2].Tags.AddRange(tags[1], tags[0]);
        metadata[3].Tags.AddRange(tags[1]);
        metadata[4].Tags.AddRange(tags[1], tags[0], tags[3]);
        metadata[5].Tags.AddRange();
        metadata[6].Tags.AddRange(tags[1]);
        DbContext.FilesMetadata.AddRange(metadata);
        await DbContext.SaveChangesAsync();

        GetTagsStatisticsHandler handler = new(DbContext);
        GetTagsStatisticsRequest request = new();
        List<TagStatisticsDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(5, result.Count);
        Assert.AreEqual(new("tag2", 6), result[0]);
        Assert.AreEqual(new("tag1", 2), result[1]);
        Assert.AreEqual(new("tag4", 1), result[2]);
        Assert.AreEqual(new("tag5", 1), result[3]);
        Assert.AreEqual(new("tag3", 0), result[4]);
    }
}