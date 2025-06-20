using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class ToggleFavoriteRequestHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task Handle_ShouldToggleFavoriteStatus()
    {
        FileMetadata metadata = new("test-file.jpg", FileType.Image, null);
        await DbContext.FilesMetadata.AddAsync(metadata);
        await DbContext.SaveChangesAsync();

        ToggleFavoriteRequestHandler handler = new(DbContext);
        ToggleFavoriteRequest request = new(metadata.Id);
        await handler.Handle(request, CancellationToken.None);
        FileMetadata result = await DbContext.FilesMetadata.FirstAsync(x => x.Id == metadata.Id);

        Assert.IsTrue(result.IsFavorite);

        await handler.Handle(request, CancellationToken.None);
        result = await DbContext.FilesMetadata.FirstAsync(x => x.Id == metadata.Id);

        Assert.IsFalse(result.IsFavorite);
    }
}