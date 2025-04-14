using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Model;

namespace TagFilesService.Library;

public class MetadataService(AppDbContext dbContext)
{
    public async Task<FileMetadata> SaveMetadata(FileMetadata metadata)
    {
        if (metadata.Id == 0)
        {
            dbContext.FilesMetadata.Add(metadata);
        }
        else
        {
            dbContext.FilesMetadata.Update(metadata);
        }

        await dbContext.SaveChangesAsync();
        return metadata;
    }

    public async Task<FileMetadata> GetMetadata(uint id)
    {
        return await GetMetadataByIdOrThrow(id);
    }

    public async Task<List<FileMetadata>> GetLastMetadataItems(int count)
    {
        return await dbContext.FilesMetadata
            .Include(x => x.Tags)
            .OrderByDescending(x => x.UploadedOn)
            .Take(count)
            .ToListAsync();
    }

    private async Task<FileMetadata> GetMetadataByIdOrThrow(uint id)
    {
        FileMetadata? metadata = await dbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstOrDefaultAsync(x => x.Id == id);
        if (metadata is null)
        {
            throw new ApplicationException($"Metadata {id} not found");
        }

        return metadata;
    }
}